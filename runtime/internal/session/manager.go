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

	mu        sync.RWMutex
	sessions  map[string]*Session
	agentCfgs map[string]config.AgentConfig

	onOutput            OutputHandler
	onPermissionRequest PermissionRequestFunc
}

// NewManager creates a session manager.
func NewManager(
	cfg config.RuntimeConfig,
	agents []config.AgentConfig,
	registry *adapter.Registry,
	onOutput OutputHandler,
	onPermissionRequest PermissionRequestFunc,
	logger *slog.Logger,
) *Manager {
	agentCfgs := make(map[string]config.AgentConfig, len(agents))
	for _, agent := range agents {
		agentCfgs[agent.ID] = agent
	}

	return &Manager{
		cfg:                 cfg,
		registry:            registry,
		logger:              logger,
		sessions:            make(map[string]*Session),
		agentCfgs:           agentCfgs,
		onOutput:            onOutput,
		onPermissionRequest: onPermissionRequest,
	}
}

// Create creates a new session for the given agent.
func (m *Manager) Create(ctx context.Context, sessionID, agentID, userID string) error {
	return m.CreateWithResume(ctx, sessionID, agentID, userID, "")
}

// CreateWithResume creates a new session, optionally resuming a native session.
func (m *Manager) CreateWithResume(ctx context.Context, sessionID, agentID, userID, resumeSessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) >= m.cfg.MaxSessions {
		return fmt.Errorf("max sessions reached (%d)", m.cfg.MaxSessions)
	}

	if _, exists := m.sessions[sessionID]; exists {
		return fmt.Errorf("session %s already exists", sessionID)
	}

	agentCfg, ok := m.agentCfgs[agentID]
	if !ok {
		return fmt.Errorf("unknown agent: %s", agentID)
	}

	adp, err := m.registry.Get(agentCfg.Profile)
	if err != nil {
		return err
	}

	agentSess, err := adp.Start(ctx, agentCfg)
	if err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	// Pre-seed native session ID for resume if provided.
	if resumeSessionID != "" {
		if rs, ok := agentSess.(adapter.ResumeSeeder); ok {
			rs.SetResumeSessionID(resumeSessionID)
		}
	}

	// Wire up permission handler if supported.
	if pr, ok := agentSess.(adapter.PermissionRequester); ok && m.onPermissionRequest != nil {
		sid := sessionID
		pr.SetPermissionHandler(func(tool, description, resource string) bool {
			return m.onPermissionRequest(sid, tool, description, resource)
		})
	}

	sess := NewSession(sessionID, agentID, userID, agentSess, m.onOutput, m.logger)
	m.sessions[sessionID] = sess

	m.logger.Info("session created", "session_id", sessionID, "agent_id", agentID, "user_id", userID,
		"resume_session_id", resumeSessionID)
	return nil
}

// GetAgentProfile returns the profile for an agent.
func (m *Manager) GetAgentProfile(agentID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if cfg, ok := m.agentCfgs[agentID]; ok {
		return cfg.Profile
	}
	return ""
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
	// Check for agent-specific idle timeout.
	if agentCfg, ok := m.agentCfgs[sess.AgentID]; ok && agentCfg.Limits != nil && agentCfg.Limits.IdleTimeout.Duration > 0 {
		idleTimeout = agentCfg.Limits.IdleTimeout.Duration
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

	// Check if the adapter is an external adapter by looking at the agent profile.
	agentCfg, isKnown := m.agentCfgs[sess.AgentID]
	if isKnown && agentCfg.Profile == "external" {
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
	if ac, ok := m.agentCfgs[sess.AgentID]; ok && ac.Limits != nil && ac.Limits.IdleTimeout.Duration > 0 {
		idleTimeout = ac.Limits.IdleTimeout.Duration
	}

	if err := sess.Send(ctx, []byte(msg), idleTimeout); err != nil {
		m.logger.Warn("deliver file path message failed", "session_id", sessionID, "error", err)
	}
}

// UpdateAgentConfig applies config overrides from the hub to the in-memory agent config.
// Only affects new sessions; existing sessions already hold a copy of the config.
func (m *Manager) UpdateAgentConfig(agentID string, security *protocol.SecurityProfile, limits *protocol.AgentLimits) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agentCfg, ok := m.agentCfgs[agentID]
	if !ok {
		return fmt.Errorf("unknown agent: %s", agentID)
	}

	if security != nil {
		// Block remote override of permission_mode to "skip" unless explicitly allowed.
		if security.PermissionMode == "skip" && !m.cfg.AllowRemotePermissionSkip {
			m.logger.Warn("blocked remote permission_mode override to 'skip'",
				"agent_id", agentID)
			return fmt.Errorf("remote override of permission_mode to 'skip' is not allowed")
		}

		if agentCfg.Security == nil {
			agentCfg.Security = &config.SecurityConfig{}
		}
		if security.Cwd != "" {
			agentCfg.Security.Cwd = security.Cwd
		}
		if security.PermissionMode != "" {
			agentCfg.Security.PermissionMode = security.PermissionMode
		}
		if security.AllowedPaths != nil {
			agentCfg.Security.AllowedPaths = security.AllowedPaths
		}
		if security.DeniedPaths != nil {
			agentCfg.Security.DeniedPaths = security.DeniedPaths
		}
		if security.AllowedTools != nil {
			agentCfg.Security.AllowedTools = security.AllowedTools
		}
		if security.EnvWhitelist != nil {
			agentCfg.Security.EnvWhitelist = security.EnvWhitelist
		}
	}

	if limits != nil {
		if agentCfg.Limits == nil {
			agentCfg.Limits = &config.AgentLimits{}
		}
		if limits.MaxSessions > 0 {
			agentCfg.Limits.MaxSessions = limits.MaxSessions
		}
		if limits.MaxOutputBytes > 0 {
			agentCfg.Limits.MaxOutputBytes = limits.MaxOutputBytes
		}
		if limits.SessionTimeout != "" {
			if d, err := time.ParseDuration(limits.SessionTimeout); err == nil {
				agentCfg.Limits.SessionTimeout = config.Duration{Duration: d}
			}
		}
		if limits.IdleTimeout != "" {
			if d, err := time.ParseDuration(limits.IdleTimeout); err == nil {
				agentCfg.Limits.IdleTimeout = config.Duration{Duration: d}
			}
		}
	}

	m.agentCfgs[agentID] = agentCfg
	return nil
}

// ActiveCount returns the number of active sessions.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// SessionInfo describes a session for external consumers (IPC, dashboard).
type SessionInfo struct {
	ID        string
	AgentID   string
	AgentName string
	UserID    string
	State     string
	CreatedAt time.Time
}

// List returns info about all active sessions.
func (m *Manager) List() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		agentName := s.AgentID
		if cfg, ok := m.agentCfgs[s.AgentID]; ok {
			agentName = cfg.Name
		}

		infos = append(infos, SessionInfo{
			ID:        s.ID,
			AgentID:   s.AgentID,
			AgentName: agentName,
			UserID:    s.UserID,
			State:     string(s.State()),
			CreatedAt: s.CreatedAt,
		})
	}
	return infos
}
