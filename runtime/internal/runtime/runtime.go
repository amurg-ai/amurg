// Package runtime is the main orchestrator that ties together the hub client,
// session manager, and adapter registry.
package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/adapter"
	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/eventbus"
	"github.com/amurg-ai/amurg/runtime/internal/hub"
	"github.com/amurg-ai/amurg/runtime/internal/ipc"
	"github.com/amurg-ai/amurg/runtime/internal/session"
)

// Runtime is the main runtime process.
type Runtime struct {
	cfg                *config.Config
	registry           *adapter.Registry
	sessions           *session.Manager
	hubClient          *hub.Client
	logger             *slog.Logger
	bus                *eventbus.Bus
	startedAt          time.Time
	mu                 sync.Mutex
	pendingPermissions map[string]chan bool
	hubConnected       bool
	hubReconnecting    bool
}

// New creates a new runtime from configuration.
// If bus is nil, events are not published.
func New(cfg *config.Config, logger *slog.Logger, bus *eventbus.Bus) *Runtime {
	registry := adapter.DefaultRegistry()

	if bus == nil {
		bus = eventbus.New()
	}

	rt := &Runtime{
		cfg:                cfg,
		registry:           registry,
		logger:             logger.With("component", "runtime", "runtime_id", cfg.Runtime.ID),
		bus:                bus,
		startedAt:          time.Now(),
		pendingPermissions: make(map[string]chan bool),
	}

	// Create session manager with output handler that forwards to hub.
	rt.sessions = session.NewManager(
		cfg.Runtime,
		cfg.Agents,
		registry,
		rt.handleAgentOutput,
		rt.handlePermissionRequest,
		logger,
	)

	// Build agent registrations for hub.
	agents := make([]protocol.AgentRegistration, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		caps, ok := protocol.KnownProfiles[agent.Profile]
		if !ok {
			caps = protocol.ProfileCaps{ExecModel: protocol.ExecInteractive}
		}

		var sec *protocol.SecurityProfile
		if agent.Security != nil {
			sec = &protocol.SecurityProfile{
				AllowedPaths:   agent.Security.AllowedPaths,
				DeniedPaths:    agent.Security.DeniedPaths,
				AllowedTools:   agent.Security.AllowedTools,
				PermissionMode: agent.Security.PermissionMode,
				Cwd:            agent.Security.Cwd,
				EnvWhitelist:   agent.Security.EnvWhitelist,
			}
		}

		agents = append(agents, protocol.AgentRegistration{
			ID:       agent.ID,
			Profile:  agent.Profile,
			Name:     agent.Name,
			Tags:     agent.Tags,
			Caps:     caps,
			Security: sec,
		})
	}

	rt.hubClient = hub.NewClient(cfg.Hub, cfg.Runtime.ID, cfg.Runtime.OrgID, agents, rt.handleHubMessage, logger)

	// Wire hub state change notifications to event bus.
	rt.hubClient.SetStateChangeHandler(func(connected, reconnecting bool) {
		rt.mu.Lock()
		rt.hubConnected = connected
		rt.hubReconnecting = reconnecting
		rt.mu.Unlock()

		if connected {
			rt.bus.PublishType(eventbus.HubConnected, nil)
		} else if reconnecting {
			rt.bus.PublishType(eventbus.HubReconnecting, nil)
		} else {
			rt.bus.PublishType(eventbus.HubDisconnected, nil)
		}
	})

	return rt
}

// Bus returns the runtime's event bus.
func (r *Runtime) Bus() *eventbus.Bus {
	return r.bus
}

// Status returns the current runtime status (implements ipc.StateProvider).
func (r *Runtime) Status() ipc.StatusResult {
	r.mu.Lock()
	connected := r.hubConnected
	reconnecting := r.hubReconnecting
	r.mu.Unlock()

	agents := make([]ipc.AgentInfo, len(r.cfg.Agents))
	for i, a := range r.cfg.Agents {
		agents[i] = ipc.AgentInfo{ID: a.ID, Name: a.Name, Profile: a.Profile, WorkDir: a.WorkDir()}
	}

	return ipc.StatusResult{
		RuntimeID:    r.cfg.Runtime.ID,
		HubURL:       r.cfg.Hub.URL,
		HubConnected: connected,
		Reconnecting: reconnecting,
		StartedAt:    r.startedAt,
		Uptime:       time.Since(r.startedAt).Truncate(time.Second).String(),
		Sessions:     r.sessions.ActiveCount(),
		MaxSessions:  r.cfg.Runtime.MaxSessions,
		Agents:       agents,
	}
}

// Sessions returns info about active sessions (implements ipc.StateProvider).
func (r *Runtime) Sessions() []ipc.SessionInfo {
	sessInfos := r.sessions.List()
	result := make([]ipc.SessionInfo, len(sessInfos))
	for i, s := range sessInfos {
		result[i] = ipc.SessionInfo{
			ID:        s.ID,
			AgentID:   s.AgentID,
			AgentName: s.AgentName,
			UserID:    s.UserID,
			State:     s.State,
			CreatedAt: s.CreatedAt,
		}
	}
	return result
}

// Run starts the runtime and blocks until the context is canceled.
func (r *Runtime) Run(ctx context.Context) error {
	r.logger.Info("starting runtime",
		"id", r.cfg.Runtime.ID,
		"agents", len(r.cfg.Agents),
		"max_sessions", r.cfg.Runtime.MaxSessions,
	)

	defer func() {
		r.logger.Info("shutting down runtime")
		r.sessions.CloseAll()
		_ = r.hubClient.Close()
	}()

	return r.hubClient.Connect(ctx)
}

// handleHubMessage processes a message from the hub.
func (r *Runtime) handleHubMessage(env protocol.Envelope) error {
	switch env.Type {
	case protocol.TypeHelloAck:
		return r.handleHelloAck(env)
	case protocol.TypeSessionCreate:
		return r.handleSessionCreate(env)
	case protocol.TypeSessionClose:
		return r.handleSessionClose(env)
	case protocol.TypeUserMessage:
		return r.handleUserMessage(env)
	case protocol.TypeStopRequest:
		return r.handleStop(env)
	case protocol.TypeFileUpload:
		return r.handleFileUpload(env)
	case protocol.TypeAgentConfigUpdate:
		return r.handleAgentConfigUpdate(env)
	case protocol.TypePermissionResponse:
		return r.handlePermissionResponse(env)
	case protocol.TypeNativeSessionsList:
		return r.handleNativeSessionsList(env)
	case protocol.TypePing:
		return r.hubClient.Send(protocol.TypePong, "", protocol.Pong{})
	default:
		r.logger.Warn("unknown message type from hub", "type", env.Type)
		return nil
	}
}

func (r *Runtime) handleHelloAck(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var ack protocol.HelloAck
	if err := json.Unmarshal(data, &ack); err != nil {
		return fmt.Errorf("unmarshal hello ack: %w", err)
	}

	if !ack.OK {
		return fmt.Errorf("hub rejected registration: %s", ack.Error)
	}

	r.logger.Info("registered with hub")
	return nil
}

func (r *Runtime) handleSessionCreate(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var req protocol.SessionCreate
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal session create: %w", err)
	}

	ctx := context.Background()
	var err error
	if req.ResumeSessionID != "" {
		err = r.sessions.CreateWithResume(ctx, req.SessionID, req.AgentID, req.UserID, req.ResumeSessionID)
	} else {
		err = r.sessions.Create(ctx, req.SessionID, req.AgentID, req.UserID)
	}

	resp := protocol.SessionCreated{
		SessionID: req.SessionID,
		OK:        err == nil,
	}
	if err != nil {
		resp.Error = err.Error()
		r.logger.Warn("session creation failed", "session_id", req.SessionID, "error", err)
	} else {
		r.bus.PublishType(eventbus.SessionCreated, map[string]string{
			"session_id": req.SessionID,
			"agent_id":   req.AgentID,
			"user_id":    req.UserID,
		})
	}

	return r.hubClient.Send(protocol.TypeSessionCreated, req.SessionID, resp)
}

func (r *Runtime) handleSessionClose(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var req protocol.SessionClose
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal session close: %w", err)
	}

	if err := r.sessions.Close(req.SessionID); err != nil {
		r.logger.Warn("session close error", "session_id", req.SessionID, "error", err)
	}

	r.bus.PublishType(eventbus.SessionClosed, map[string]string{
		"session_id": req.SessionID,
	})

	return nil
}

func (r *Runtime) handleUserMessage(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var msg protocol.UserMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("unmarshal user message: %w", err)
	}

	// Signal turn started.
	_ = r.hubClient.Send(protocol.TypeTurnStarted, msg.SessionID, protocol.TurnStarted{
		SessionID:    msg.SessionID,
		InResponseTo: msg.MessageID,
	})

	ctx := context.Background()
	err := r.sessions.Send(ctx, msg.SessionID, []byte(msg.Content))

	// Lazy session recreation: if the session is gone (runtime restarted)
	// but the hub forwarded enough metadata, recreate it with the native handle.
	if err != nil && msg.AgentID != "" {
		r.logger.Info("attempting lazy session recreation",
			"session_id", msg.SessionID, "agent_id", msg.AgentID,
			"native_handle", msg.NativeHandle)
		if createErr := r.sessions.CreateWithResume(ctx, msg.SessionID, msg.AgentID, msg.UserID, msg.NativeHandle); createErr == nil {
			err = r.sessions.Send(ctx, msg.SessionID, []byte(msg.Content))
		} else {
			r.logger.Warn("lazy session recreation failed", "session_id", msg.SessionID, "error", createErr)
		}
	}

	if err != nil {
		r.logger.Warn("send to agent failed", "session_id", msg.SessionID, "error", err)

		// Send turn completed with error.
		_ = r.hubClient.Send(protocol.TypeTurnCompleted, msg.SessionID, protocol.TurnCompleted{
			SessionID:    msg.SessionID,
			InResponseTo: msg.MessageID,
		})
	}

	return nil
}

func (r *Runtime) handleStop(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var req protocol.StopRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal stop: %w", err)
	}

	err := r.sessions.Stop(req.SessionID)
	ack := protocol.StopAck{
		SessionID: req.SessionID,
		OK:        err == nil,
	}
	if err != nil {
		ack.Error = err.Error()
	}

	return r.hubClient.Send(protocol.TypeStopAck, req.SessionID, ack)
}

// handleFileUpload handles file.upload from hub (user uploaded a file).
func (r *Runtime) handleFileUpload(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var upload protocol.FileUpload
	if err := json.Unmarshal(data, &upload); err != nil {
		return fmt.Errorf("unmarshal file upload: %w", err)
	}

	// Decode base64 content.
	fileData, err := base64.StdEncoding.DecodeString(upload.Data)
	if err != nil {
		return fmt.Errorf("decode file data: %w", err)
	}

	// Save to runtime disk: {files_dir}/{session_id}/{file_id}/{filename}
	dir := filepath.Join(r.cfg.Runtime.FileStoragePath, upload.SessionID, upload.Metadata.FileID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create file dir: %w", err)
	}
	filePath := filepath.Join(dir, upload.Metadata.Name)
	if err := os.WriteFile(filePath, fileData, 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	r.logger.Info("file saved", "session_id", upload.SessionID, "file_id", upload.Metadata.FileID, "path", filePath)

	// Deliver to adapter via session manager.
	r.sessions.DeliverFile(upload.SessionID, filePath, upload.Metadata)

	return nil
}

// handleAgentConfigUpdate applies a config override from the hub.
func (r *Runtime) handleAgentConfigUpdate(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var update protocol.AgentConfigUpdate
	if err := json.Unmarshal(data, &update); err != nil {
		return fmt.Errorf("unmarshal agent config update: %w", err)
	}

	// Log security details before applying config override.
	if update.Security != nil {
		r.logger.Info("applying agent config override",
			"agent_id", update.AgentID,
			"permission_mode", update.Security.PermissionMode,
			"cwd", update.Security.Cwd,
		)
	}

	err := r.sessions.UpdateAgentConfig(update.AgentID, update.Security, update.Limits)

	ack := protocol.AgentConfigAck{
		AgentID: update.AgentID,
		OK:      err == nil,
	}
	if err != nil {
		ack.Error = err.Error()
		r.logger.Warn("agent config update failed", "agent_id", update.AgentID, "error", err)
	} else {
		r.logger.Info("agent config updated", "agent_id", update.AgentID)
	}

	return r.hubClient.Send(protocol.TypeAgentConfigAck, "", ack)
}

// handleAgentOutput is called by the session manager when the agent produces output.
func (r *Runtime) handleAgentOutput(sessionID string, output adapter.Output, final bool) {
	if final {
		tc := protocol.TurnCompleted{SessionID: sessionID}
		if output.ExitCode != nil {
			tc.ExitCode = output.ExitCode
		}
		// Report the native handle so the hub can persist it for session resilience.
		tc.NativeHandle = r.sessions.GetNativeHandle(sessionID)
		_ = r.hubClient.Send(protocol.TypeTurnCompleted, sessionID, tc)
		return
	}

	// Handle file output from adapter.
	if output.FileName != "" {
		fileData := output.Data
		fileID := uuid.New().String()
		mimeType := output.FileMimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		_ = r.hubClient.Send(protocol.TypeFileAvailable, sessionID, protocol.FileAvailable{
			SessionID: sessionID,
			Metadata: protocol.FileMetadata{
				FileID:   fileID,
				Name:     output.FileName,
				MimeType: mimeType,
				Size:     int64(len(fileData)),
			},
			Data: base64.StdEncoding.EncodeToString(fileData),
		})
		return
	}

	_ = r.hubClient.Send(protocol.TypeAgentOutput, sessionID, protocol.AgentOutput{
		SessionID: sessionID,
		Channel:   output.Channel,
		Content:   string(output.Data),
	})
}

// handlePermissionRequest is called by the session manager when an adapter needs user permission.
func (r *Runtime) handlePermissionRequest(sessionID, tool, description, resource string) bool {
	requestID := uuid.New().String()

	ch := make(chan bool, 1)
	r.mu.Lock()
	r.pendingPermissions[requestID] = ch
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.pendingPermissions, requestID)
		r.mu.Unlock()
	}()

	// Send permission request to hub.
	_ = r.hubClient.Send(protocol.TypePermissionRequest, sessionID, protocol.PermissionRequest{
		SessionID:   sessionID,
		RequestID:   requestID,
		Tool:        tool,
		Description: description,
		Resource:    resource,
	})

	// Block waiting for response with safety timeout.
	select {
	case approved := <-ch:
		return approved
	case <-time.After(120 * time.Second):
		r.logger.Warn("permission request timed out locally", "request_id", requestID)
		return false
	}
}

func (r *Runtime) handleNativeSessionsList(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var req protocol.NativeSessionsList
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal native sessions list: %w", err)
	}

	// Look up the adapter for this agent's profile and check if it supports listing.
	profile := r.sessions.GetAgentProfile(req.AgentID)
	adp, err := r.registry.Get(profile)
	if err != nil {
		return r.hubClient.Send(protocol.TypeNativeSessionsResponse, "", protocol.NativeSessionsResponse{
			AgentID:   req.AgentID,
			RequestID: req.RequestID,
			Error:     fmt.Sprintf("unknown profile: %s", profile),
		})
	}

	lister, ok := adp.(adapter.NativeSessionLister)
	if !ok {
		return r.hubClient.Send(protocol.TypeNativeSessionsResponse, "", protocol.NativeSessionsResponse{
			AgentID:   req.AgentID,
			RequestID: req.RequestID,
			Error:     "agent profile does not support native sessions",
		})
	}

	entries, err := lister.ListNativeSessions()
	resp := protocol.NativeSessionsResponse{
		AgentID:   req.AgentID,
		RequestID: req.RequestID,
	}
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Sessions = make([]protocol.NativeSession, len(entries))
		for i, e := range entries {
			resp.Sessions[i] = protocol.NativeSession{
				SessionID:    e.SessionID,
				Summary:      e.Summary,
				FirstPrompt:  e.FirstPrompt,
				MessageCount: e.MessageCount,
				ProjectPath:  e.ProjectPath,
				GitBranch:    e.GitBranch,
				Created:      e.Created,
				Modified:     e.Modified,
			}
		}
	}

	return r.hubClient.Send(protocol.TypeNativeSessionsResponse, "", resp)
}

func (r *Runtime) handlePermissionResponse(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var resp protocol.PermissionResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("unmarshal permission response: %w", err)
	}

	r.mu.Lock()
	ch, ok := r.pendingPermissions[resp.RequestID]
	r.mu.Unlock()

	if ok {
		ch <- resp.Approved
	} else {
		r.logger.Warn("permission response for unknown request_id", "request_id", resp.RequestID)
	}
	return nil
}
