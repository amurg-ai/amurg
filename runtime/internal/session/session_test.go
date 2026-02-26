package session

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/amurg-ai/amurg/runtime/internal/adapter"
)

// mockAgentSession is a minimal mock implementing adapter.AgentSession.
type mockAgentSession struct {
	mu      sync.Mutex
	sendErr error
	stopErr error
	closed  bool
	outCh   chan adapter.Output
}

func newMockAgent() *mockAgentSession {
	return &mockAgentSession{
		outCh: make(chan adapter.Output, 10),
	}
}

func (m *mockAgentSession) Send(_ context.Context, _ []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sendErr
}

func (m *mockAgentSession) Output() <-chan adapter.Output {
	return m.outCh
}

func (m *mockAgentSession) Wait() error { return nil }

func (m *mockAgentSession) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopErr
}

func (m *mockAgentSession) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockAgentSession) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewSession(t *testing.T) {
	agent := newMockAgent()
	handler := func(string, adapter.Output, bool) {}

	sess := NewSession("sess-1", "ep-1", "user-1", agent, handler, testLogger())

	if sess.ID != "sess-1" {
		t.Errorf("expected ID sess-1, got %s", sess.ID)
	}
	if sess.AgentID != "ep-1" {
		t.Errorf("expected AgentID ep-1, got %s", sess.AgentID)
	}
	if sess.UserID != "user-1" {
		t.Errorf("expected UserID user-1, got %s", sess.UserID)
	}
	if sess.State() != StateActive {
		t.Errorf("expected initial state active, got %s", sess.State())
	}
}

func TestSession_State_Initial(t *testing.T) {
	agent := newMockAgent()
	handler := func(string, adapter.Output, bool) {}
	sess := NewSession("s1", "e1", "u1", agent, handler, testLogger())

	if sess.State() != StateActive {
		t.Errorf("expected initial state %s, got %s", StateActive, sess.State())
	}
}

func TestSession_Send_SetsResponding(t *testing.T) {
	agent := newMockAgent()
	handler := func(string, adapter.Output, bool) {}
	sess := NewSession("s1", "e1", "u1", agent, handler, testLogger())

	err := sess.Send(context.Background(), []byte("hello"), 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sess.State() != StateResponding {
		t.Errorf("expected state responding after Send, got %s", sess.State())
	}

	// Close the output channel so drainOutput finishes.
	close(agent.outCh)
	// Wait a bit for the goroutine to finish.
	time.Sleep(50 * time.Millisecond)
}

func TestSession_Send_ErrorRestoresActive(t *testing.T) {
	agent := newMockAgent()
	agent.sendErr = context.DeadlineExceeded
	handler := func(string, adapter.Output, bool) {}
	sess := NewSession("s1", "e1", "u1", agent, handler, testLogger())

	err := sess.Send(context.Background(), []byte("hello"), 5*time.Second)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if sess.State() != StateActive {
		t.Errorf("expected state to revert to active on error, got %s", sess.State())
	}
}

func TestSession_DrainOutput_ForwardsMessages(t *testing.T) {
	agent := newMockAgent()

	var mu sync.Mutex
	var received []adapter.Output
	var finals []bool
	handler := func(_ string, out adapter.Output, final bool) {
		mu.Lock()
		received = append(received, out)
		finals = append(finals, final)
		mu.Unlock()
	}

	sess := NewSession("s1", "e1", "u1", agent, handler, testLogger())

	// Send a message to start draining.
	err := sess.Send(context.Background(), []byte("go"), 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Push some output then close the channel.
	agent.outCh <- adapter.Output{Channel: "stdout", Data: []byte("line 1")}
	agent.outCh <- adapter.Output{Channel: "stderr", Data: []byte("err 1")}
	close(agent.outCh)

	// Wait for drain to complete.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// We expect: stdout output (not final), stderr output (not final), final system message.
	if len(received) != 3 {
		t.Fatalf("expected 3 outputs, got %d", len(received))
	}

	if received[0].Channel != "stdout" {
		t.Errorf("expected first output channel stdout, got %s", received[0].Channel)
	}
	if finals[0] {
		t.Error("first output should not be final")
	}

	if received[1].Channel != "stderr" {
		t.Errorf("expected second output channel stderr, got %s", received[1].Channel)
	}
	if finals[1] {
		t.Error("second output should not be final")
	}

	// Third should be the final system message.
	if received[2].Channel != "system" {
		t.Errorf("expected final output channel system, got %s", received[2].Channel)
	}
	if !finals[2] {
		t.Error("third output should be final")
	}

	// Session state should be back to active.
	if sess.State() != StateActive {
		t.Errorf("expected state active after drain, got %s", sess.State())
	}
}

func TestSession_DrainOutput_IdleTimeout(t *testing.T) {
	agent := newMockAgent()

	var mu sync.Mutex
	var received []adapter.Output
	var finals []bool
	handler := func(_ string, out adapter.Output, final bool) {
		mu.Lock()
		received = append(received, out)
		finals = append(finals, final)
		mu.Unlock()
	}

	sess := NewSession("s1", "e1", "u1", agent, handler, testLogger())

	// Use a very short idle timeout.
	err := sess.Send(context.Background(), []byte("go"), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Push one output, then do nothing. Idle timeout should trigger.
	agent.outCh <- adapter.Output{Channel: "stdout", Data: []byte("data")}

	// Wait for idle timeout to fire.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should have the data output (not final) and then a final system message from idle timeout.
	if len(received) < 2 {
		t.Fatalf("expected at least 2 outputs, got %d", len(received))
	}

	lastIdx := len(received) - 1
	if received[lastIdx].Channel != "system" {
		t.Errorf("expected final output to be system, got %s", received[lastIdx].Channel)
	}
	if !finals[lastIdx] {
		t.Error("last output should be final (idle timeout)")
	}

	if sess.State() != StateActive {
		t.Errorf("expected state active after idle timeout, got %s", sess.State())
	}

	// Clean up - close channel so goroutine exits if still running.
	close(agent.outCh)
}

func TestSession_DrainOutput_ExitCode(t *testing.T) {
	agent := newMockAgent()

	var mu sync.Mutex
	var received []adapter.Output
	var finals []bool
	handler := func(_ string, out adapter.Output, final bool) {
		mu.Lock()
		received = append(received, out)
		finals = append(finals, final)
		mu.Unlock()
	}

	sess := NewSession("s1", "e1", "u1", agent, handler, testLogger())

	err := sess.Send(context.Background(), []byte("go"), 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Push output with exit code - should immediately be final.
	exitCode := 0
	agent.outCh <- adapter.Output{Channel: "stdout", Data: []byte("done"), ExitCode: &exitCode}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 output (with exit code), got %d", len(received))
	}

	if !finals[0] {
		t.Error("output with exit code should be final")
	}
	if received[0].ExitCode == nil || *received[0].ExitCode != 0 {
		t.Error("expected exit code 0")
	}

	if sess.State() != StateActive {
		t.Errorf("expected state active after exit code, got %s", sess.State())
	}

	close(agent.outCh)
}

func TestSession_Close(t *testing.T) {
	agent := newMockAgent()
	handler := func(string, adapter.Output, bool) {}
	sess := NewSession("s1", "e1", "u1", agent, handler, testLogger())

	err := sess.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sess.State() != StateClosed {
		t.Errorf("expected state closed, got %s", sess.State())
	}
	if !agent.isClosed() {
		t.Error("expected agent to be closed")
	}
}

func TestSession_Stop(t *testing.T) {
	agent := newMockAgent()
	handler := func(string, adapter.Output, bool) {}
	sess := NewSession("s1", "e1", "u1", agent, handler, testLogger())

	err := sess.Stop()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
