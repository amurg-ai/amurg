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

// CodexAdapter implements the codex profile.
// It uses Codex CLI's `exec --json` mode for JSONL streaming,
// spawning a new process per Send() call.
type CodexAdapter struct{}

func (a *CodexAdapter) Start(ctx context.Context, cfg config.EndpointConfig) (AgentSession, error) {
	cxCfg := cfg.Codex
	if cxCfg == nil {
		cxCfg = &config.CodexConfig{}
	}
	if cxCfg.Command == "" {
		cxCfg.Command = "codex"
	}

	sess := &codexSession{
		ctx:      ctx,
		cfg:      *cxCfg,
		security: cfg.Security,
		output:   make(chan Output, 64),
	}
	return sess, nil
}

// codexSession manages a Codex CLI conversation across multiple Send() calls.
type codexSession struct {
	ctx      context.Context
	cfg      config.CodexConfig
	security *config.SecurityConfig
	hasSent  bool

	output chan Output
	mu     sync.Mutex
	cmd    *exec.Cmd
	done   chan struct{}
	closed bool

	permHandler func(tool, description, resource string) bool
}

func (s *codexSession) Send(ctx context.Context, input []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	s.mu.Unlock()

	// Build command: codex exec --json [flags] "prompt"
	// or: codex exec resume --last --json [flags] "prompt"
	args := []string{"exec"}

	// Resume after first send.
	if s.hasSent {
		args = append(args, "resume", "--last")
	}
	s.hasSent = true

	args = append(args, "--json", "--color", "never")

	// Permission mode from security config.
	permMode := s.cfg.ApprovalMode
	if s.security != nil && s.security.PermissionMode != "" {
		switch s.security.PermissionMode {
		case "skip":
			permMode = "never"
		case "strict":
			permMode = "untrusted"
		case "auto":
			permMode = "on-request"
		}
	}
	if permMode == "skip" {
		// Map legacy "skip" to "never" for Codex.
		permMode = "never"
	}
	if permMode != "" {
		args = append(args, "--ask-for-approval", permMode)
	}

	// Sandbox mode.
	sandboxMode := s.cfg.SandboxMode
	if sandboxMode != "" {
		args = append(args, "--sandbox", sandboxMode)
	}

	// Model.
	if s.cfg.Model != "" {
		args = append(args, "--model", s.cfg.Model)
	}

	// Working directory.
	workDir := s.cfg.WorkDir
	if s.security != nil && s.security.Cwd != "" {
		workDir = s.security.Cwd
	}
	if workDir != "" {
		args = append(args, "--cd", workDir)
	}

	// The prompt text is the final argument.
	args = append(args, string(input))

	cmd := exec.CommandContext(ctx, s.cfg.Command, args...)
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
		return fmt.Errorf("start codex process: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.done = make(chan struct{})
	s.mu.Unlock()

	done := s.done

	var wg sync.WaitGroup
	wg.Add(2)

	// Read JSONL from stdout.
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			s.handleCodexEvent(line)
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

// handleCodexEvent parses a single JSONL event from codex exec --json output.
func (s *codexSession) handleCodexEvent(line []byte) {
	var event struct {
		Type string          `json:"type"`
		Item json.RawMessage `json:"item,omitempty"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		// Not valid JSON — emit as raw stdout.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
		return
	}

	switch event.Type {
	case "item.completed", "item.updated":
		// Parse item to extract content.
		var item struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content,omitempty"`
			// For command execution items.
			Command string `json:"command,omitempty"`
			Output  string `json:"output,omitempty"`
			// For file change items.
			FilePath string `json:"file_path,omitempty"`
			Diff     string `json:"diff,omitempty"`
		}
		if err := json.Unmarshal(event.Item, &item); err == nil {
			switch item.Type {
			case "agent_message", "message":
				for _, c := range item.Content {
					if c.Type == "text" && c.Text != "" {
						s.output <- Output{Channel: "stdout", Data: []byte(c.Text)}
					}
				}
			case "command_execution":
				if item.Command != "" {
					s.output <- Output{Channel: "stdout", Data: []byte("$ " + item.Command)}
				}
				if item.Output != "" {
					s.output <- Output{Channel: "stdout", Data: []byte(item.Output)}
				}
			case "file_change":
				if item.FilePath != "" && item.Diff != "" {
					s.output <- Output{Channel: "stdout", Data: []byte("--- " + item.FilePath + "\n" + item.Diff)}
				}
			}
		}

	case "error", "turn.failed":
		// Emit the full event as stderr.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stderr", Data: cp}

	default:
		// Other events (turn.started, turn.completed, item.started) — emit raw for transparency.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
	}
}

func (s *codexSession) Output() <-chan Output {
	return s.output
}

func (s *codexSession) Wait() error {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}

func (s *codexSession) Stop() error {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

func (s *codexSession) Close() error {
	s.mu.Lock()
	s.closed = true
	cmd := s.cmd
	done := s.done
	s.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
	if done != nil {
		<-done
	}
	close(s.output)
	return nil
}

func (s *codexSession) ExitCode() *int {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		return &code
	}
	return nil
}

func (s *codexSession) SetPermissionHandler(handler func(tool, description, resource string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permHandler = handler
}
