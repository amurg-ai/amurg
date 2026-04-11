package session

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/adapter"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// mockAdapter implements adapter.Adapter for testing.
type mockAdapter struct {
	startErr error
}

func (m *mockAdapter) Start(_ context.Context, _ config.AgentConfig) (adapter.AgentSession, error) {
	if m.startErr != nil {
		return nil, m.startErr
	}
	return newMockAgent(), nil
}

type historyAdapter struct{}

func (a *historyAdapter) Start(_ context.Context, _ config.AgentConfig) (adapter.AgentSession, error) {
	return &historyAgentSession{mockAgentSession: newMockAgent()}, nil
}

type capturePromptAdapter struct {
	lastConfig config.AgentConfig
}

func (a *capturePromptAdapter) Start(_ context.Context, cfg config.AgentConfig) (adapter.AgentSession, error) {
	a.lastConfig = cfg
	return newMockAgent(), nil
}

type historyAgentSession struct {
	*mockAgentSession
	resumeID string
}

func (s *historyAgentSession) SetResumeSessionID(id string) {
	s.resumeID = id
}

func (s *historyAgentSession) LoadNativeHistory() []adapter.Output {
	return []adapter.Output{
		{Channel: "history_user", Data: []byte("remember this")},
	}
}

type restartOnSecurityAdapter struct {
	session *restartOnSecuritySession
}

func (a *restartOnSecurityAdapter) Start(_ context.Context, _ config.AgentConfig) (adapter.AgentSession, error) {
	a.session = &restartOnSecuritySession{
		mockAgentSession: newMockAgent(),
		nativeHandle:     "native-123",
	}
	return a.session, nil
}

type restartOnSecuritySession struct {
	*mockAgentSession
	nativeHandle    string
	resumeID        string
	stopCalls       int
	updatedSecurity *config.SecurityConfig
}

func (s *restartOnSecuritySession) UpdateSecurity(security *config.SecurityConfig) (restartRequired bool) {
	s.updatedSecurity = security
	return true
}

func (s *restartOnSecuritySession) NativeHandle() string {
	return s.nativeHandle
}

func (s *restartOnSecuritySession) SetResumeSessionID(id string) {
	s.resumeID = id
}

func (s *restartOnSecuritySession) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopCalls++
	return s.stopErr
}

func testManagerConfig() config.RuntimeConfig {
	return config.RuntimeConfig{
		ID:          "test-runtime",
		MaxSessions: 3,
		IdleTimeout: config.Duration{Duration: 30 * time.Second},
	}
}

func testAgents() []config.AgentConfig {
	return []config.AgentConfig{
		{
			ID:      "ep-1",
			Name:    "Test CLI",
			Profile: "test-profile",
		},
		{
			ID:      "ep-2",
			Name:    "Test Job",
			Profile: "test-profile",
		},
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()

	registry := adapter.NewRegistry()
	registry.Register("test-profile", &mockAdapter{})

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := func(string, adapter.Output, bool) {}

	return NewManager(testManagerConfig(), testAgents(), registry, handler, nil, logger)
}

func TestManager_Create(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1", "standard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.ActiveCount() != 1 {
		t.Errorf("expected 1 active session, got %d", m.ActiveCount())
	}
}

func TestManager_Create_DuplicateSession(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1", "standard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = m.Create(context.Background(), "sess-1", "ep-1", "user-1", "standard")
	if err == nil {
		t.Fatal("expected error for duplicate session, got nil")
	}
}

func TestManager_Create_UnknownAgent(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "unknown-ep", "user-1", "standard")
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
}

func TestManager_Create_MaxSessionsEnforced(t *testing.T) {
	m := newTestManager(t)

	// Config allows max 3 sessions.
	for i := 0; i < 3; i++ {
		err := m.Create(context.Background(), "sess-"+string(rune('a'+i)), "ep-1", "user-1", "standard")
		if err != nil {
			t.Fatalf("unexpected error creating session %d: %v", i, err)
		}
	}

	// Fourth should fail.
	err := m.Create(context.Background(), "sess-x", "ep-1", "user-1", "standard")
	if err == nil {
		t.Fatal("expected error when max sessions exceeded, got nil")
	}
}

func TestManager_Get(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1", "standard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sess, ok := m.Get("sess-1")
	if !ok {
		t.Fatal("expected to find session")
	}
	if sess.ID != "sess-1" {
		t.Errorf("expected session ID sess-1, got %s", sess.ID)
	}

	_, ok = m.Get("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent session")
	}
}

func TestManager_Close(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1", "standard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = m.Close("sess-1")
	if err != nil {
		t.Fatalf("unexpected error closing: %v", err)
	}

	if m.ActiveCount() != 0 {
		t.Errorf("expected 0 active sessions after close, got %d", m.ActiveCount())
	}

	_, ok := m.Get("sess-1")
	if ok {
		t.Error("expected session to be removed after close")
	}
}

func TestManager_Close_NotFound(t *testing.T) {
	m := newTestManager(t)

	err := m.Close("nonexistent")
	if err == nil {
		t.Fatal("expected error closing nonexistent session, got nil")
	}
}

func TestManager_Stop(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1", "standard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = m.Stop("sess-1")
	if err != nil {
		t.Fatalf("unexpected error stopping: %v", err)
	}
}

func TestManager_Stop_NotFound(t *testing.T) {
	m := newTestManager(t)

	err := m.Stop("nonexistent")
	if err == nil {
		t.Fatal("expected error stopping nonexistent session, got nil")
	}
}

func TestManager_SendInteractive(t *testing.T) {
	m := newTestManager(t)

	if err := m.Create(context.Background(), "sess-1", "ep-1", "user-1", "standard"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := m.SendInteractive(context.Background(), "sess-1", []byte("reply")); err != nil {
		t.Fatalf("unexpected error sending interactive input: %v", err)
	}

	sess, ok := m.Get("sess-1")
	if !ok {
		t.Fatal("expected session to exist")
	}
	if sess.State() != StateResponding {
		t.Fatalf("expected responding state, got %s", sess.State())
	}
}

func TestManager_CloseAll(t *testing.T) {
	m := newTestManager(t)

	for i := 0; i < 3; i++ {
		err := m.Create(context.Background(), "sess-"+string(rune('a'+i)), "ep-1", "user-1", "standard")
		if err != nil {
			t.Fatalf("unexpected error creating session %d: %v", i, err)
		}
	}

	if m.ActiveCount() != 3 {
		t.Fatalf("expected 3 active sessions, got %d", m.ActiveCount())
	}

	m.CloseAll()

	if m.ActiveCount() != 0 {
		t.Errorf("expected 0 active sessions after CloseAll, got %d", m.ActiveCount())
	}
}

func TestManager_ActiveCount(t *testing.T) {
	m := newTestManager(t)

	if m.ActiveCount() != 0 {
		t.Errorf("expected 0 initially, got %d", m.ActiveCount())
	}

	_ = m.Create(context.Background(), "s1", "ep-1", "user-1", "standard")
	if m.ActiveCount() != 1 {
		t.Errorf("expected 1, got %d", m.ActiveCount())
	}

	_ = m.Create(context.Background(), "s2", "ep-2", "user-1", "standard")
	if m.ActiveCount() != 2 {
		t.Errorf("expected 2, got %d", m.ActiveCount())
	}

	_ = m.Close("s1")
	if m.ActiveCount() != 1 {
		t.Errorf("expected 1 after close, got %d", m.ActiveCount())
	}
}

func TestManager_Send(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1", "standard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = m.Send(context.Background(), "sess-1", []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error sending: %v", err)
	}

	// Verify session is now responding.
	sess, ok := m.Get("sess-1")
	if !ok {
		t.Fatal("session not found")
	}
	if sess.State() != StateResponding {
		t.Errorf("expected state responding, got %s", sess.State())
	}

	// Clean up: close the output channel so drainOutput exits.
	agent := sess.agent.(*mockAgentSession)
	close(agent.outCh)
	time.Sleep(50 * time.Millisecond)
}

func TestManager_Send_NotFound(t *testing.T) {
	m := newTestManager(t)

	err := m.Send(context.Background(), "nonexistent", []byte("hello"))
	if err == nil {
		t.Fatal("expected error sending to nonexistent session, got nil")
	}
}

func TestManager_CreateWithResume_HistoryReplayDoesNotEmitFinal(t *testing.T) {
	registry := adapter.NewRegistry()
	registry.Register("history-profile", &historyAdapter{})

	var (
		mu      sync.Mutex
		outputs []adapter.Output
		finals  []bool
	)
	handler := func(_ string, out adapter.Output, final bool) {
		mu.Lock()
		defer mu.Unlock()
		outputs = append(outputs, out)
		finals = append(finals, final)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(config.RuntimeConfig{ID: "test-runtime", MaxSessions: 3}, []config.AgentConfig{
		{ID: "hist-1", Name: "History Agent", Profile: "history-profile"},
	}, registry, handler, nil, logger)

	if err := m.CreateWithResume(context.Background(), "sess-1", "hist-1", "user-1", "native-1", "deep_debug"); err != nil {
		t.Fatalf("CreateWithResume: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(outputs) != 2 {
		t.Fatalf("expected 2 history outputs, got %d", len(outputs))
	}
	for i, final := range finals {
		if final {
			t.Fatalf("history output %d unexpectedly marked final", i)
		}
	}
	if outputs[1].Channel != "system" {
		t.Fatalf("expected final replay message on system channel, got %q", outputs[1].Channel)
	}
}

func TestManager_Create_PassesPromptProfileToAdapter(t *testing.T) {
	registry := adapter.NewRegistry()
	capture := &capturePromptAdapter{}
	registry.Register("capture-profile", capture)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := func(string, adapter.Output, bool) {}
	m := NewManager(testManagerConfig(), []config.AgentConfig{
		{ID: "ep-1", Name: "Capture Agent", Profile: "capture-profile"},
	}, registry, handler, nil, logger)

	if err := m.Create(context.Background(), "sess-1", "ep-1", "user-1", "deep_debug"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if capture.lastConfig.PromptProfile != "deep_debug" {
		t.Fatalf("expected prompt profile deep_debug, got %q", capture.lastConfig.PromptProfile)
	}
}

func TestManager_UpdateAgentConfig_PermissionChangeSeedsResumeSessionID(t *testing.T) {
	registry := adapter.NewRegistry()
	adp := &restartOnSecurityAdapter{}
	registry.Register("restart-profile", adp)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(config.RuntimeConfig{ID: "test-runtime", MaxSessions: 3}, []config.AgentConfig{
		{
			ID:       "restart-1",
			Name:     "Restart Agent",
			Profile:  "restart-profile",
			Security: &config.SecurityConfig{PermissionMode: "auto"},
		},
	}, registry, func(string, adapter.Output, bool) {}, nil, logger)

	if err := m.Create(context.Background(), "sess-1", "restart-1", "user-1", "standard"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if adp.session == nil {
		t.Fatal("expected adapter session to be created")
	}

	if err := m.UpdateAgentConfig("restart-1", &protocol.SecurityProfile{PermissionMode: "plan"}, nil); err != nil {
		t.Fatalf("UpdateAgentConfig: %v", err)
	}

	if adp.session.updatedSecurity == nil {
		t.Fatal("expected updated security config")
	}
	if adp.session.updatedSecurity.PermissionMode != "plan" {
		t.Fatalf("expected permission mode plan, got %q", adp.session.updatedSecurity.PermissionMode)
	}
	if adp.session.resumeID != adp.session.nativeHandle {
		t.Fatalf("expected resume ID %q, got %q", adp.session.nativeHandle, adp.session.resumeID)
	}
	if adp.session.stopCalls != 1 {
		t.Fatalf("expected 1 stop call, got %d", adp.session.stopCalls)
	}
}
