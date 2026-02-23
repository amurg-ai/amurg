package session

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/adapter"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// PermissionRequestFunc is called when an adapter needs user permission.
type PermissionRequestFunc func(sessionID, tool, description, resource string) bool

// Manager tracks all active sessions and enforces runtime limits.
type Manager struct {
	cfg      config.RuntimeConfig
	registry *adapter.Registry
	logger   *slog.Logger

	mu       sync.RWMutex
	sessions map[string]*Session
	epCfgs   map[string]config.EndpointConfig

	onOutput            OutputHandler
	onPermissionRequest PermissionRequestFunc
}

// NewManager creates a session manager.
func NewManager(
	cfg config.RuntimeConfig,
	endpoints []config.EndpointConfig,
	registry *adapter.Registry,
	onOutput OutputHandler,
	onPermissionRequest PermissionRequestFunc,
	logger *slog.Logger,
) *Manager {
	epCfgs := make(map[string]config.EndpointConfig, len(endpoints))
	for _, ep := range endpoints {
		epCfgs[ep.ID] = ep
	}

	return &Manager{
		cfg:                 cfg,
		registry:            registry,
		logger:              logger,
		sessions:            make(map[string]*Session),
		epCfgs:              epCfgs,
		onOutput:            onOutput,
		onPermissionRequest: onPermissionRequest,
	}
}

// Create creates a new session for the given endpoint.
func (m *Manager) Create(ctx context.Context, sessionID, endpointID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) >= m.cfg.MaxSessions {
		return fmt.Errorf("max sessions reached (%d)", m.cfg.MaxSessions)
	}

	if _, exists := m.sessions[sessionID]; exists {
		return fmt.Errorf("session %s already exists", sessionID)
	}

	epCfg, ok := m.epCfgs[endpointID]
	if !ok {
		return fmt.Errorf("unknown endpoint: %s", endpointID)
	}

	adp, err := m.registry.Get(epCfg.Profile)
	if err != nil {
		return err
	}

	agent, err := adp.Start(ctx, epCfg)
	if err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	// Wire up permission handler if supported.
	if pr, ok := agent.(adapter.PermissionRequester); ok && m.onPermissionRequest != nil {
		sid := sessionID
		pr.SetPermissionHandler(func(tool, description, resource string) bool {
			return m.onPermissionRequest(sid, tool, description, resource)
		})
	}

	sess := NewSession(sessionID, endpointID, userID, agent, m.onOutput, m.logger)
	m.sessions[sessionID] = sess

	m.logger.Info("session created", "session_id", sessionID, "endpoint_id", endpointID, "user_id", userID)
	return nil
}

// Send delivers a user message to a session's agent.
func (m *Manager) Send(ctx context.Context, sessionID string, input []byte) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	idleTimeout := m.cfg.IdleTimeout.Duration
	// Check for endpoint-specific idle timeout.
	if epCfg, ok := m.epCfgs[sess.EndpointID]; ok && epCfg.Limits != nil && epCfg.Limits.IdleTimeout.Duration > 0 {
		idleTimeout = epCfg.Limits.IdleTimeout.Duration
	}

	return sess.Send(ctx, input, idleTimeout)
}

// Stop requests stop for a session.
func (m *Manager) Stop(sessionID string) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	return sess.Stop()
}

// Close closes a session and removes it from the manager.
func (m *Manager) Close(sessionID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	return sess.Close()
}

// Get returns a session by ID.
func (m *Manager) Get(sessionID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[sessionID]
	return sess, ok
}

// CloseAll closes all active sessions.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()

	for _, s := range sessions {
		if err := s.Close(); err != nil {
			m.logger.Warn("error closing session", "session_id", s.ID, "error", err)
		}
	}
}

// DeliverFile delivers an uploaded file to a session's adapter.
// For external adapters it sends a file.input JSON-Lines message.
// For other adapters it sends a text message with the file path.
func (m *Manager) DeliverFile(sessionID, filePath string, meta protocol.FileMetadata) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		m.logger.Warn("deliver file: session not found", "session_id", sessionID)
		return
	}

	// Check if the adapter is an external adapter by looking at the endpoint profile.
	epCfg, isKnown := m.epCfgs[sess.EndpointID]
	if isKnown && epCfg.Profile == "external" {
		// External adapters get native file protocol via DeliverFileToExternal.
		if fd, ok := sess.agent.(adapter.FileDeliverer); ok {
			if err := fd.DeliverFile(filePath, meta.Name, meta.MimeType); err != nil {
				m.logger.Warn("deliver file to external adapter failed", "session_id", sessionID, "error", err)
			}
			return
		}
	}

	// Non-external adapters get a text message with the file path.
	ctx := context.Background()
	msg := fmt.Sprintf("[File uploaded: %s (%s, %d bytes)] Path: %s", meta.Name, meta.MimeType, meta.Size, filePath)

	idleTimeout := m.cfg.IdleTimeout.Duration
	if epCfg, ok := m.epCfgs[sess.EndpointID]; ok && epCfg.Limits != nil && epCfg.Limits.IdleTimeout.Duration > 0 {
		idleTimeout = epCfg.Limits.IdleTimeout.Duration
	}

	if err := sess.Send(ctx, []byte(msg), idleTimeout); err != nil {
		m.logger.Warn("deliver file path message failed", "session_id", sessionID, "error", err)
	}
}

// UpdateEndpointConfig applies config overrides from the hub to the in-memory endpoint config.
// Only affects new sessions; existing sessions already hold a copy of the config.
func (m *Manager) UpdateEndpointConfig(endpointID string, security *protocol.SecurityProfile, limits *protocol.EndpointLimits) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	epCfg, ok := m.epCfgs[endpointID]
	if !ok {
		return fmt.Errorf("unknown endpoint: %s", endpointID)
	}

	if security != nil {
		if epCfg.Security == nil {
			epCfg.Security = &config.SecurityConfig{}
		}
		if security.Cwd != "" {
			epCfg.Security.Cwd = security.Cwd
		}
		if security.PermissionMode != "" {
			epCfg.Security.PermissionMode = security.PermissionMode
		}
		if security.AllowedPaths != nil {
			epCfg.Security.AllowedPaths = security.AllowedPaths
		}
		if security.DeniedPaths != nil {
			epCfg.Security.DeniedPaths = security.DeniedPaths
		}
		if security.AllowedTools != nil {
			epCfg.Security.AllowedTools = security.AllowedTools
		}
		if security.EnvWhitelist != nil {
			epCfg.Security.EnvWhitelist = security.EnvWhitelist
		}
	}

	if limits != nil {
		if epCfg.Limits == nil {
			epCfg.Limits = &config.EndpointLimits{}
		}
		if limits.MaxSessions > 0 {
			epCfg.Limits.MaxSessions = limits.MaxSessions
		}
		if limits.MaxOutputBytes > 0 {
			epCfg.Limits.MaxOutputBytes = limits.MaxOutputBytes
		}
		if limits.SessionTimeout != "" {
			if d, err := time.ParseDuration(limits.SessionTimeout); err == nil {
				epCfg.Limits.SessionTimeout = config.Duration{Duration: d}
			}
		}
		if limits.IdleTimeout != "" {
			if d, err := time.ParseDuration(limits.IdleTimeout); err == nil {
				epCfg.Limits.IdleTimeout = config.Duration{Duration: d}
			}
		}
	}

	m.epCfgs[endpointID] = epCfg
	return nil
}

// ActiveCount returns the number of active sessions.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}
