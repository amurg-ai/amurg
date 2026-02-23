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
	"github.com/amurg-ai/amurg/runtime/internal/hub"
	"github.com/amurg-ai/amurg/runtime/internal/session"
)

// Runtime is the main runtime process.
type Runtime struct {
	cfg                *config.Config
	registry           *adapter.Registry
	sessions           *session.Manager
	hubClient          *hub.Client
	logger             *slog.Logger
	mu                 sync.Mutex
	pendingPermissions map[string]chan bool
}

// New creates a new runtime from configuration.
func New(cfg *config.Config, logger *slog.Logger) *Runtime {
	registry := adapter.DefaultRegistry()

	rt := &Runtime{
		cfg:                cfg,
		registry:           registry,
		logger:             logger.With("component", "runtime", "runtime_id", cfg.Runtime.ID),
		pendingPermissions: make(map[string]chan bool),
	}

	// Create session manager with output handler that forwards to hub.
	rt.sessions = session.NewManager(
		cfg.Runtime,
		cfg.Endpoints,
		registry,
		rt.handleAgentOutput,
		rt.handlePermissionRequest,
		logger,
	)

	// Build endpoint registrations for hub.
	endpoints := make([]protocol.EndpointRegistration, 0, len(cfg.Endpoints))
	for _, ep := range cfg.Endpoints {
		caps, ok := protocol.KnownProfiles[ep.Profile]
		if !ok {
			caps = protocol.ProfileCaps{ExecModel: protocol.ExecInteractive}
		}

		var sec *protocol.SecurityProfile
		if ep.Security != nil {
			sec = &protocol.SecurityProfile{
				AllowedPaths:   ep.Security.AllowedPaths,
				DeniedPaths:    ep.Security.DeniedPaths,
				AllowedTools:   ep.Security.AllowedTools,
				PermissionMode: ep.Security.PermissionMode,
				Cwd:            ep.Security.Cwd,
				EnvWhitelist:   ep.Security.EnvWhitelist,
			}
		}

		endpoints = append(endpoints, protocol.EndpointRegistration{
			ID:       ep.ID,
			Profile:  ep.Profile,
			Name:     ep.Name,
			Tags:     ep.Tags,
			Caps:     caps,
			Security: sec,
		})
	}

	rt.hubClient = hub.NewClient(cfg.Hub, cfg.Runtime.ID, cfg.Runtime.OrgID, endpoints, rt.handleHubMessage, logger)

	return rt
}

// Run starts the runtime and blocks until the context is canceled.
func (r *Runtime) Run(ctx context.Context) error {
	r.logger.Info("starting runtime",
		"id", r.cfg.Runtime.ID,
		"endpoints", len(r.cfg.Endpoints),
		"max_sessions", r.cfg.Runtime.MaxSessions,
	)

	defer func() {
		r.logger.Info("shutting down runtime")
		r.sessions.CloseAll()
		r.hubClient.Close()
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
	case protocol.TypeEndpointConfigUpdate:
		return r.handleEndpointConfigUpdate(env)
	case protocol.TypePermissionResponse:
		return r.handlePermissionResponse(env)
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
	err := r.sessions.Create(ctx, req.SessionID, req.EndpointID, req.UserID)

	resp := protocol.SessionCreated{
		SessionID: req.SessionID,
		OK:        err == nil,
	}
	if err != nil {
		resp.Error = err.Error()
		r.logger.Warn("session creation failed", "session_id", req.SessionID, "error", err)
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

	return nil
}

func (r *Runtime) handleUserMessage(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var msg protocol.UserMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("unmarshal user message: %w", err)
	}

	// Signal turn started.
	r.hubClient.Send(protocol.TypeTurnStarted, msg.SessionID, protocol.TurnStarted{
		SessionID:    msg.SessionID,
		InResponseTo: msg.MessageID,
	})

	ctx := context.Background()
	if err := r.sessions.Send(ctx, msg.SessionID, []byte(msg.Content)); err != nil {
		r.logger.Warn("send to agent failed", "session_id", msg.SessionID, "error", err)

		// Send turn completed with error.
		r.hubClient.Send(protocol.TypeTurnCompleted, msg.SessionID, protocol.TurnCompleted{
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

// handleEndpointConfigUpdate applies a config override from the hub.
func (r *Runtime) handleEndpointConfigUpdate(env protocol.Envelope) error {
	data, _ := json.Marshal(env.Payload)
	var update protocol.EndpointConfigUpdate
	if err := json.Unmarshal(data, &update); err != nil {
		return fmt.Errorf("unmarshal endpoint config update: %w", err)
	}

	// Log security details before applying config override.
	if update.Security != nil {
		r.logger.Info("applying endpoint config override",
			"endpoint_id", update.EndpointID,
			"permission_mode", update.Security.PermissionMode,
			"cwd", update.Security.Cwd,
		)
	}

	err := r.sessions.UpdateEndpointConfig(update.EndpointID, update.Security, update.Limits)

	ack := protocol.EndpointConfigAck{
		EndpointID: update.EndpointID,
		OK:         err == nil,
	}
	if err != nil {
		ack.Error = err.Error()
		r.logger.Warn("endpoint config update failed", "endpoint_id", update.EndpointID, "error", err)
	} else {
		r.logger.Info("endpoint config updated", "endpoint_id", update.EndpointID)
	}

	return r.hubClient.Send(protocol.TypeEndpointConfigAck, "", ack)
}

// handleAgentOutput is called by the session manager when the agent produces output.
func (r *Runtime) handleAgentOutput(sessionID string, output adapter.Output, final bool) {
	if final {
		tc := protocol.TurnCompleted{SessionID: sessionID}
		if output.ExitCode != nil {
			tc.ExitCode = output.ExitCode
		}
		r.hubClient.Send(protocol.TypeTurnCompleted, sessionID, tc)
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

		r.hubClient.Send(protocol.TypeFileAvailable, sessionID, protocol.FileAvailable{
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

	r.hubClient.Send(protocol.TypeAgentOutput, sessionID, protocol.AgentOutput{
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
	r.hubClient.Send(protocol.TypePermissionRequest, sessionID, protocol.PermissionRequest{
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
