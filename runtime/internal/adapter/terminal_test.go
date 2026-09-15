package adapter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

func terminalTestConfig(t *testing.T) config.AgentConfig {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := fmt.Sprintf("amurg-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	return config.AgentConfig{
		SessionKey: t.Name(),
		TMux:       &config.TMuxConfig{SocketName: socket, WorkDir: t.TempDir(), Command: "sh", Args: []string{"-c", `stty -echo; echo $$ > pid; printf 'READY\n'; while IFS= read -r line; do printf 'GOT:%s\n' "$line"; done`}},
	}
}

func waitTerminal(t *testing.T, s *terminalSession, predicate func(protocol.TerminalOutput) bool) {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		select {
		case out := <-s.TerminalOutput():
			if predicate(out) {
				return
			}
		case <-timer.C:
			t.Fatal("timed out waiting for terminal output")
		}
	}
}

func waitTerminalText(t *testing.T, s *terminalSession, text string) {
	t.Helper()
	var output string
	waitTerminal(t, s, func(out protocol.TerminalOutput) bool {
		output += string(out.Data)
		return strings.Contains(output, text)
	})
}

func startTerminalTest(t *testing.T, cfg config.AgentConfig) *terminalSession {
	t.Helper()
	a, err := (&TMuxAdapter{}).Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := a.(*terminalSession)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestTerminalPersistenceAndPassthrough(t *testing.T) {
	cfg := terminalTestConfig(t)
	s := startTerminalTest(t, cfg)
	ctx := context.Background()
	if err := s.Attach(ctx, 80, 24); err != nil {
		t.Fatal(err)
	}
	generation := ""
	waitTerminal(t, s, func(out protocol.TerminalOutput) bool {
		if out.Kind == "reset" {
			generation = out.Generation
			return true
		}
		return false
	})
	waitTerminalText(t, s, "READY")
	pid, err := os.ReadFile(filepath.Join(cfg.TMux.WorkDir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	// Raw input does not get an implicit Enter. We split a line across calls.
	if err := s.Input(ctx, generation, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := s.Input(ctx, generation, []byte(" world\r")); err != nil {
		t.Fatal(err)
	}
	waitTerminalText(t, s, "GOT:hello world")
	if err := s.Resize(ctx, generation, 100, 32); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// A fresh runtime wrapper resolves the same tmux session and same agent PID.
	s2 := startTerminalTest(t, cfg)
	if err := s2.Attach(ctx, 100, 32); err != nil {
		t.Fatal(err)
	}
	waitTerminalText(t, s2, "GOT:hello world") // tmux repaints retained terminal state
	after, err := os.ReadFile(filepath.Join(cfg.TMux.WorkDir, "pid"))
	if err != nil || string(after) != string(pid) {
		t.Fatalf("process changed across detach: %q -> %q (%v)", pid, after, err)
	}
	if err := s2.Input(ctx, generation, []byte("stale\r")); err == nil {
		t.Fatal("accepted stale input generation")
	}
	generation = s2.client.generation
	if err := s2.Input(ctx, generation, []byte("again\r")); err != nil {
		t.Fatal(err)
	}
	waitTerminalText(t, s2, "GOT:again")
}

func TestTerminalAttachExistingAndCloseBackpressure(t *testing.T) {
	cfg := terminalTestConfig(t)
	cfg.TMux.Command = ""
	cfg.TMux.Args = nil
	cfg.TMux.SessionName = "existing"
	s := startTerminalTest(t, cfg)
	if err := s.Attach(context.Background(), 80, 24); err == nil {
		t.Fatal("created missing external session")
	}
	if _, err := s.run(context.Background(), "new-session", "-d", "-s", "existing", "yes"); err != nil {
		t.Fatal(err)
	}
	if err := s.Attach(context.Background(), 80, 24); err != nil {
		t.Fatal(err)
	}
	// Stop consuming a noisy pane. Close must release the blocked reader.
	time.Sleep(250 * time.Millisecond)
	done := make(chan struct{})
	go func() { _ = s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked behind output")
	}
	if _, err := s.run(context.Background(), "has-session", "-t", "=existing"); err != nil {
		t.Fatal("close killed external session", err)
	}
}
