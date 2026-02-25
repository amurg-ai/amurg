package adapter

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// GitHubCopilotAdapter implements the github-copilot profile.
// It uses the new Copilot CLI's `-p --silent` mode,
// spawning a new process per Send() call and using --continue for session continuity.
type GitHubCopilotAdapter struct{}

func (a *GitHubCopilotAdapter) Start(ctx context.Context, cfg config.EndpointConfig) (AgentSession, error) {
	copCfg := cfg.Copilot
	if copCfg == nil {
		copCfg = &config.CopilotConfig{}
	}
	if copCfg.Command == "" {
		copCfg.Command = "copilot"
	}

	sess := &copilotSession{
		ctx:      ctx,
		cfg:      *copCfg,
		security: cfg.Security,
		output:   make(chan Output, 64),
	}
	return sess, nil
}

// copilotSession manages a Copilot CLI conversation across multiple Send() calls.
// Each Send() spawns a new `copilot -p` process; --continue maintains session continuity.
type copilotSession struct {
	ctx      context.Context
	cfg      config.CopilotConfig
	security *config.SecurityConfig
	hasSent  bool // true after first Send() — use --continue for subsequent

	output chan Output
	mu     sync.Mutex
	cmd    *exec.Cmd
	done   chan struct{}
	closed bool

	permHandler func(tool, description, resource string) bool
}

func (s *copilotSession) Send(ctx context.Context, input []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	s.mu.Unlock()

	args := []string{"-p", "--silent", "--no-color"}

	// Permission mode from security config.
	permMode := ""
	if s.security != nil && s.security.PermissionMode != "" {
		permMode = s.security.PermissionMode
	}
	if permMode == "skip" {
		args = append(args, "--allow-all")
	}

	// Model.
	if s.cfg.Model != "" {
		args = append(args, "--model", s.cfg.Model)
	}

	// Allowed tools — merge security config and profile config.
	allowedTools := s.cfg.AllowedTools
	if s.security != nil && len(s.security.AllowedTools) > 0 {
		allowedTools = s.security.AllowedTools
	}
	for _, tool := range allowedTools {
		args = append(args, "--allow-tool", tool)
	}

	// Denied tools.
	deniedTools := s.cfg.DeniedTools
	if s.security != nil && len(s.security.DeniedPaths) > 0 {
		// Map denied paths as denied tools for directory access control.
		deniedTools = append(deniedTools, s.security.DeniedPaths...)
	}
	for _, tool := range deniedTools {
		args = append(args, "--deny-tool", tool)
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

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start copilot process: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.done = make(chan struct{})
	s.mu.Unlock()

	done := s.done

	// Read stdout and stderr in background.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			cp := make([]byte, len(line))
			copy(cp, line)
			s.output <- Output{Channel: "stdout", Data: cp}
		}
	}()

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

	// Wait for process to exit, then signal turn complete.
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

func (s *copilotSession) Output() <-chan Output {
	return s.output
}

func (s *copilotSession) Wait() error {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}

func (s *copilotSession) Stop() error {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

func (s *copilotSession) Close() error {
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

func (s *copilotSession) ExitCode() *int {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		return &code
	}
	return nil
}

func (s *copilotSession) SetPermissionHandler(handler func(tool, description, resource string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permHandler = handler
}
