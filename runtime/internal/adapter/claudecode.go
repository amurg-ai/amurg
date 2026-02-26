package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// ClaudeCodeAdapter implements the claude-code profile.
// It uses Claude Code's `-p --output-format stream-json` mode,
// spawning a new process per Send() call and using --resume for continuity.
type ClaudeCodeAdapter struct{}

func (a *ClaudeCodeAdapter) Start(ctx context.Context, cfg config.AgentConfig) (AgentSession, error) {
	ccCfg := cfg.ClaudeCode
	if ccCfg == nil {
		ccCfg = &config.ClaudeCodeConfig{}
	}
	if ccCfg.Command == "" {
		ccCfg.Command = "claude"
	}

	sess := &claudeCodeSession{
		ctx:      ctx,
		cfg:      *ccCfg,
		security: cfg.Security,
		output:   make(chan Output, 64),
	}
	return sess, nil
}

// claudeCodeSession manages a Claude Code conversation across multiple Send() calls.
// Each Send() spawns a new `claude -p` process; --resume maintains continuity.
type claudeCodeSession struct {
	ctx         context.Context
	cfg         config.ClaudeCodeConfig
	security    *config.SecurityConfig
	sessionID   string // Claude Code's native session ID for --resume
	permHandler func(tool, description, resource string) bool

	output chan Output
	mu     sync.Mutex
	cmd    *exec.Cmd
	done   chan struct{} // closed when current process exits
	closed bool
}

func (s *claudeCodeSession) Send(ctx context.Context, input []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	s.mu.Unlock()

	args := []string{"-p", "--output-format", "stream-json", "--verbose"}

	// Permission mode - security config takes precedence.
	permMode := s.cfg.PermissionMode
	if s.security != nil && s.security.PermissionMode != "" {
		permMode = s.security.PermissionMode
	}
	if permMode == "dangerously-skip-permissions" || permMode == "skip" {
		args = append(args, "--dangerously-skip-permissions")
	}

	// Model.
	if s.cfg.Model != "" {
		args = append(args, "--model", s.cfg.Model)
	}

	// Max turns.
	if s.cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(s.cfg.MaxTurns))
	}

	// Allowed tools - security config takes precedence.
	allowedTools := s.cfg.AllowedTools
	if s.security != nil && len(s.security.AllowedTools) > 0 {
		allowedTools = s.security.AllowedTools
	}
	for _, tool := range allowedTools {
		args = append(args, "--allowedTools", tool)
	}

	// System prompt.
	if s.cfg.SystemPrompt != "" {
		args = append(args, "--system-prompt", s.cfg.SystemPrompt)
	}

	// Resume with native session ID.
	if s.sessionID != "" {
		args = append(args, "--resume", s.sessionID)
	}

	// The prompt text is the final argument.
	args = append(args, string(input))

	cmd := exec.CommandContext(ctx, s.cfg.Command, args...)
	// Working directory - security.Cwd overrides profile config.
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
		return fmt.Errorf("start claude process: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.done = make(chan struct{})
	s.mu.Unlock()

	done := s.done

	// Read stderr in background (non-NDJSON, just raw lines).
	var wg sync.WaitGroup
	wg.Add(1)
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

	// Read NDJSON from stdout.
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			s.handleStreamEvent(line)
		}
	}()

	// Wait for process to exit, then signal turn complete.
	go func() {
		wg.Wait()
		waitErr := cmd.Wait()

		// Compute exit code.
		var exitCode *int
		if cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			exitCode = &code
		}

		// Emit final output to signal turn complete.
		if waitErr != nil && exitCode == nil {
			code := 1
			exitCode = &code
		}
		s.output <- Output{Channel: "system", Data: nil, ExitCode: exitCode}

		close(done)
	}()

	return nil
}

// handleStreamEvent parses a single NDJSON line from claude stream-json output.
func (s *claudeCodeSession) handleStreamEvent(line []byte) {
	var event struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		Result    string `json:"result"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		// Not valid JSON — emit as raw stdout.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
		return
	}

	switch event.Type {
	case "result":
		// Save session ID for --resume on next Send().
		if event.SessionID != "" {
			s.mu.Lock()
			s.sessionID = event.SessionID
			s.mu.Unlock()
		}
		if event.Result != "" {
			s.output <- Output{Channel: "stdout", Data: []byte(event.Result)}
		}

	case "assistant":
		// Parse message.content[].text
		var msg struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(event.Message, &msg); err == nil {
			for _, c := range msg.Content {
				if c.Type == "text" && c.Text != "" {
					s.output <- Output{Channel: "stdout", Data: []byte(c.Text)}
				}
			}
		}

	case "system":
		// System events — emit as system channel.
		var msgStr string
		if err := json.Unmarshal(event.Message, &msgStr); err == nil && msgStr != "" {
			s.output <- Output{Channel: "system", Data: []byte(msgStr)}
		}

	default:
		// Other event types — emit raw JSON line for transparency.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
	}
}

func (s *claudeCodeSession) SetPermissionHandler(handler func(tool, description, resource string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permHandler = handler
}

func (s *claudeCodeSession) Output() <-chan Output {
	return s.output
}

func (s *claudeCodeSession) Wait() error {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}

func (s *claudeCodeSession) Stop() error {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

func (s *claudeCodeSession) Close() error {
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

func (s *claudeCodeSession) ExitCode() *int {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		return &code
	}
	return nil
}
