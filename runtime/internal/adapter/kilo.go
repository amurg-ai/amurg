package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// KiloAdapter implements the kilo-code profile.
// It uses Kilo Code CLI's `run --auto --json` mode,
// spawning a new process per Send() call.
type KiloAdapter struct{}

func (a *KiloAdapter) Start(ctx context.Context, cfg config.AgentConfig) (AgentSession, error) {
	kiloCfg := cfg.Kilo
	if kiloCfg == nil {
		kiloCfg = &config.KiloConfig{}
	}
	if kiloCfg.Command == "" {
		kiloCfg.Command = "kilo"
	}

	sess := &kiloSession{
		ctx:      ctx,
		cfg:      *kiloCfg,
		security: cfg.Security,
		output:   make(chan Output, 64),
	}
	return sess, nil
}

// kiloSession manages a Kilo Code CLI conversation across multiple Send() calls.
type kiloSession struct {
	ctx      context.Context
	cfg      config.KiloConfig
	security *config.SecurityConfig
	hasSent  bool

	output chan Output
	mu     sync.Mutex
	cmd    *exec.Cmd
	done   chan struct{}
	closed bool

	permHandler func(tool, description, resource string) bool
}

func (s *kiloSession) Send(ctx context.Context, input []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	s.mu.Unlock()

	args := []string{"run", "--auto", "--json"}

	// Permission mode from security config.
	permMode := ""
	if s.security != nil && s.security.PermissionMode != "" {
		permMode = s.security.PermissionMode
	}
	if permMode == "skip" {
		args = append(args, "--yolo")
	}

	// Session continuity: use --continue after first send.
	if s.hasSent {
		args = append(args, "--continue")
	}
	s.hasSent = true

	// The prompt text is the final argument.
	args = append(args, string(input))

	cmd := exec.CommandContext(ctx, s.cfg.Command, args...)

	// Working directory.
	workDir := s.cfg.WorkDir
	if s.security != nil && s.security.Cwd != "" {
		workDir = s.security.Cwd
	}
	if workDir != "" {
		cmd.Dir = workDir
	}

	cmd.Env = os.Environ()
	for k, v := range s.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// Pass model and provider via env if configured.
	if s.cfg.Model != "" {
		cmd.Env = append(cmd.Env, "KILO_MODEL="+s.cfg.Model)
	}
	if s.cfg.Provider != "" {
		cmd.Env = append(cmd.Env, "KILO_PROVIDER="+s.cfg.Provider)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start kilo process: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.done = make(chan struct{})
	s.mu.Unlock()

	done := s.done

	var wg sync.WaitGroup
	wg.Add(2)

	// Read JSON messages from stdout.
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			s.handleKiloMessage(line)
		}
	}()

	// Read stderr as raw lines.
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			cp := make([]byte, len(line))
			copy(cp, line)
			s.output <- Output{Channel: "stderr", Data: cp}
		}
	}()

	// Wait for process exit.
	go func() {
		wg.Wait()
		waitErr := cmd.Wait()
		_ = waitErr

		var exitCode *int
		if cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			exitCode = &code
		}
		if waitErr != nil && exitCode == nil {
			code := 1
			exitCode = &code
		}
		s.output <- Output{Channel: "system", Data: nil, ExitCode: exitCode}

		close(done)
	}()

	return nil
}

// handleKiloMessage parses a single JSON message from kilo run --auto --json.
func (s *kiloSession) handleKiloMessage(line []byte) {
	var msg struct {
		Type      string `json:"type"`
		Content   string `json:"content,omitempty"`
		Timestamp string `json:"timestamp,omitempty"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		// Not valid JSON — emit as raw stdout.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
		return
	}

	if msg.Content != "" {
		s.output <- Output{Channel: "stdout", Data: []byte(msg.Content)}
	} else {
		// Emit the raw JSON for event types without textual content.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
	}
}

func (s *kiloSession) Output() <-chan Output {
	return s.output
}

func (s *kiloSession) Wait() error {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}

func (s *kiloSession) Stop() error {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

func (s *kiloSession) Close() error {
	s.mu.Lock()
	s.closed = true
	cmd := s.cmd
	done := s.done
	s.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if done != nil {
		<-done
	}
	close(s.output)
	return nil
}

func (s *kiloSession) ExitCode() *int {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		return &code
	}
	return nil
}

func (s *kiloSession) SetPermissionHandler(handler func(tool, description, resource string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permHandler = handler
}
