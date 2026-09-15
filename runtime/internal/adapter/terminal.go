package adapter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/creack/pty"
	"github.com/google/uuid"
)

// TerminalSession relays a native terminal independently of chat turn handling.
type TerminalSession interface {
	Attach(context.Context, uint16, uint16) error
	Input(context.Context, string, []byte) error
	Resize(context.Context, string, uint16, uint16) error
	TerminalOutput() <-chan protocol.TerminalOutput
}

// TMuxAdapter owns only a disposable tmux client. The tmux server owns the agent.
type TMuxAdapter struct{}

// CheckTerminalSupport is used at startup so missing dependencies fail before
// agents are advertised as available to the hub.
func CheckTerminalSupport() error {
	if err := ensureTMuxInstalled(); err != nil {
		return fmt.Errorf("persistent interactive sessions need tmux: rerun the Amurg installer (use WSL on Windows): %w", err)
	}
	return nil
}

func (*TMuxAdapter) Start(_ context.Context, cfg config.AgentConfig) (AgentSession, error) {
	if cfg.TMux == nil {
		launch, err := terminalLaunchConfig(cfg)
		if err != nil {
			return nil, err
		}
		cfg.TMux = &launch
	}
	if err := CheckTerminalSupport(); err != nil {
		return nil, err
	}
	tc := *cfg.TMux
	if (tc.Command == "") == (tc.SessionName == "") {
		return nil, fmt.Errorf("tmux requires exactly one of command or session_name")
	}
	if tc.WorkDir == "" {
		tc.WorkDir = cfg.WorkDir()
	}
	if cfg.Security != nil && cfg.Security.Cwd != "" {
		tc.WorkDir = cfg.Security.Cwd
	}
	if tc.WorkDir != "" {
		info, err := os.Stat(tc.WorkDir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("tmux work_dir is not a directory: %s", tc.WorkDir)
		}
	}
	name := tc.SessionName
	if name == "" {
		if cfg.SessionKey == "" {
			return nil, fmt.Errorf("tmux requires a stable session identity")
		}
		sum := sha256.Sum256([]byte(cfg.SessionKey))
		name = fmt.Sprintf("amurg-%x", sum[:16])
	}
	return &terminalSession{cfg: tc, name: name, terminalOutput: make(chan protocol.TerminalOutput, 64), output: make(chan Output), done: make(chan struct{})}, nil
}

type terminalClient struct {
	cmd        *exec.Cmd
	tty        *os.File
	stop       chan struct{}
	done       chan struct{}
	generation string
}

type terminalSession struct {
	cfg            config.TMuxConfig
	name           string
	mu             sync.Mutex // serializes attach, resize, input and close
	client         *terminalClient
	closed         bool
	terminalOutput chan protocol.TerminalOutput
	output         chan Output // chat output is deliberately unused
	done           chan struct{}
}

func (s *terminalSession) command(ctx context.Context, args ...string) *exec.Cmd {
	if s.cfg.SocketName != "" {
		args = append([]string{"-L", s.cfg.SocketName}, args...)
	}
	if s.cfg.SocketName == "amurg" {
		// Amurg owns this server; personal tmux config must not alter its runtime.
		args = append([]string{"-f", "/dev/null"}, args...)
	}
	cmd := exec.CommandContext(ctx, "tmux", args...)
	// A runtime launched inside tmux must be able to attach as an independent client.
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TERM=") && !strings.HasPrefix(e, "TMUX_PANE=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	return cmd
}

func (s *terminalSession) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := s.command(ctx, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

func (s *terminalSession) ensureSession(ctx context.Context, cols, rows uint16) error {
	target := "=" + s.name // exact session match
	if _, err := s.run(ctx, "has-session", "-t", target); err == nil {
		return nil
	}
	if s.cfg.SessionName != "" {
		return fmt.Errorf("configured tmux session %q is not running", s.name)
	}
	args := []string{"new-session", "-d", "-s", s.name, "-x", fmt.Sprint(cols), "-y", fmt.Sprint(rows)}
	if s.cfg.WorkDir != "" {
		args = append(args, "-c", s.cfg.WorkDir)
	}
	// Scope environment changes to the launched process, not the shared tmux server.
	command := []string{"env", "-u", "CLAUDECODE", "-u", "CLAUDE_CODE_ENTRYPOINT"}
	keys := make([]string, 0, len(s.cfg.Env))
	for key := range s.cfg.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		command = append(command, key+"="+s.cfg.Env[key])
	}
	command = append(command, s.cfg.Command)
	command = append(command, s.cfg.Args...)
	args = append(args, "exec "+tmuxCommandString(command))
	if _, err := s.run(ctx, args...); err != nil {
		return err
	}
	// Keep status bars out of the remote agent view. This affects only our session.
	_, err := s.run(ctx, "set-option", "-t", s.name, "status", "off")
	return err
}

func validTerminalSize(cols, rows uint16) bool {
	return cols >= 2 && cols <= 500 && rows >= 2 && rows <= 300
}

func (s *terminalSession) Attach(ctx context.Context, cols, rows uint16) error {
	if !validTerminalSize(cols, rows) {
		return fmt.Errorf("terminal dimensions must be 2..500 columns and 2..300 rows")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("terminal session closed")
	}
	if err := s.ensureSession(ctx, cols, rows); err != nil {
		return err
	}
	if s.cfg.SocketName == "amurg" {
		// Native TUIs use focus notifications; configure our managed server so
		// applications never ask users to edit a personal tmux configuration.
		if _, err := s.run(ctx, "set-option", "-s", "focus-events", "on"); err != nil {
			return err
		}
	}
	s.detachLocked()
	// Do not tie the agent lifetime to a request context. Only this attach client
	// is local to the runtime, and Close explicitly tears it down.
	cmd := s.command(context.Background(), "attach-session", "-t", "="+s.name)
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return fmt.Errorf("attach tmux client: %w", err)
	}
	c := &terminalClient{cmd: cmd, tty: tty, generation: uuid.NewString(), stop: make(chan struct{}), done: make(chan struct{})}
	s.client = c
	// Reset is ordered before every byte from the new client.
	select {
	case s.terminalOutput <- protocol.TerminalOutput{Kind: "reset", Generation: c.generation, Cols: cols, Rows: rows}:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = tty.Close()
		_ = cmd.Wait()
		s.client = nil
		return ctx.Err()
	}
	go s.readClient(c)
	return nil
}

func (s *terminalSession) readClient(c *terminalClient) {
	defer close(c.done)
	defer c.tty.Close()
	buf := make([]byte, 16*1024)
	for {
		n, err := c.tty.Read(buf)
		if n > 0 {
			out := protocol.TerminalOutput{Kind: "output", Generation: c.generation, Data: append([]byte(nil), buf[:n]...)}
			select {
			case s.terminalOutput <- out:
			case <-c.stop:
				_ = c.cmd.Wait()
				return
			}
		}
		if err != nil {
			break
		}
	}
	_ = c.cmd.Wait()
	select {
	case s.terminalOutput <- protocol.TerminalOutput{Kind: "detached", Generation: c.generation}:
	case <-c.stop:
	}
}

func (s *terminalSession) detachLocked() {
	if s.client == nil {
		return
	}
	c := s.client
	close(c.stop)
	_ = c.cmd.Process.Kill() // only the attach client, never the server or pane
	_ = c.tty.Close()
	<-c.done
	s.client = nil
}

func (s *terminalSession) Input(ctx context.Context, generation string, data []byte) error {
	if len(data) > 32*1024 {
		return fmt.Errorf("terminal input exceeds 32 KiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.client
	if s.closed || c == nil || generation != c.generation {
		return fmt.Errorf("terminal needs reattachment")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.tty.Write(data)
	return err
}

func (s *terminalSession) Resize(_ context.Context, generation string, cols, rows uint16) error {
	if !validTerminalSize(cols, rows) {
		return fmt.Errorf("invalid terminal dimensions")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.client == nil || s.client.generation != generation {
		return fmt.Errorf("terminal needs reattachment")
	}
	return pty.Setsize(s.client.tty, &pty.Winsize{Cols: cols, Rows: rows})
}

func (s *terminalSession) TerminalOutput() <-chan protocol.TerminalOutput { return s.terminalOutput }
func (s *terminalSession) Output() <-chan Output                          { return s.output }
func (s *terminalSession) Wait() error                                    { <-s.done; return nil }
func (s *terminalSession) NativeHandle() string                           { return "tmux:" + s.name }
func (s *terminalSession) Send(context.Context, []byte) error {
	return fmt.Errorf("use the terminal input channel for tmux sessions")
}
func (s *terminalSession) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil || s.closed {
		return nil
	}
	_, err := s.client.tty.Write([]byte{3})
	return err
}
func (s *terminalSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.detachLocked()
	close(s.terminalOutput)
	close(s.output)
	close(s.done)
	return nil
}
