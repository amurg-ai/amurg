package session

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

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

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.ActiveCount() != 1 {
		t.Errorf("expected 1 active session, got %d", m.ActiveCount())
	}
}

func TestManager_Create_DuplicateSession(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = m.Create(context.Background(), "sess-1", "ep-1", "user-1")
	if err == nil {
		t.Fatal("expected error for duplicate session, got nil")
	}
}

func TestManager_Create_UnknownAgent(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "unknown-ep", "user-1")
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
}

func TestManager_Create_MaxSessionsEnforced(t *testing.T) {
	m := newTestManager(t)

	// Config allows max 3 sessions.
	for i := 0; i < 3; i++ {
		err := m.Create(context.Background(), "sess-"+string(rune('a'+i)), "ep-1", "user-1")
		if err != nil {
			t.Fatalf("unexpected error creating session %d: %v", i, err)
		}
	}

	// Fourth should fail.
	err := m.Create(context.Background(), "sess-x", "ep-1", "user-1")
	if err == nil {
		t.Fatal("expected error when max sessions exceeded, got nil")
	}
}

func TestManager_Get(t *testing.T) {
	m := newTestManager(t)

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1")
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

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1")
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

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1")
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

func TestManager_CloseAll(t *testing.T) {
	m := newTestManager(t)

	for i := 0; i < 3; i++ {
		err := m.Create(context.Background(), "sess-"+string(rune('a'+i)), "ep-1", "user-1")
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

	_ = m.Create(context.Background(), "s1", "ep-1", "user-1")
	if m.ActiveCount() != 1 {
		t.Errorf("expected 1, got %d", m.ActiveCount())
	}

	_ = m.Create(context.Background(), "s2", "ep-2", "user-1")
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

	err := m.Create(context.Background(), "sess-1", "ep-1", "user-1")
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
