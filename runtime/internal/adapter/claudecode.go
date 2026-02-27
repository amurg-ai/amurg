package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	// Working directory — validated with fallback.
	if dir := resolveWorkDir(s.cfg.WorkDir, s.security); dir != "" {
		cmd.Dir = dir
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

// truncateStr limits s to maxLen characters, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// handleStreamEvent parses a single NDJSON line from claude stream-json output.
func (s *claudeCodeSession) handleStreamEvent(line []byte) {
	var event struct {
		Type      string          `json:"type"`
		SessionID string          `json:"session_id"`
		Result    string          `json:"result"`
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
		// Parse message.content[] blocks.
		var msg struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		}
		if err := json.Unmarshal(event.Message, &msg); err == nil {
			for _, c := range msg.Content {
				switch c.Type {
				case "text":
					if c.Text != "" {
						s.output <- Output{Channel: "stdout", Data: []byte(c.Text)}
					}
				case "tool_use":
					summary := fmt.Sprintf("**Tool: %s**", c.Name)
					if len(c.Input) > 0 && string(c.Input) != "{}" && string(c.Input) != "null" {
						var inputMap map[string]any
						if err := json.Unmarshal(c.Input, &inputMap); err == nil {
							if cmd, ok := inputMap["command"].(string); ok {
								summary += fmt.Sprintf("\n`%s`", truncateStr(cmd, 200))
							} else if fp, ok := inputMap["file_path"].(string); ok {
								summary += fmt.Sprintf(" `%s`", fp)
							} else if pat, ok := inputMap["pattern"].(string); ok {
								summary += fmt.Sprintf(" `%s`", pat)
							} else if query, ok := inputMap["query"].(string); ok {
								summary += fmt.Sprintf(" `%s`", truncateStr(query, 100))
							}
						}
					}
					s.output <- Output{Channel: "stdout", Data: []byte(summary)}
				case "thinking":
					if c.Text != "" {
						s.output <- Output{Channel: "stdout", Data: []byte("*" + truncateStr(c.Text, 500) + "*")}
					}
				// server_tool_use, etc. — skip silently
				}
			}
		}

	case "user":
		// User events are tool_result echoes. Skip to avoid noise.

	case "system":
		// System events — emit as system channel.
		var msgStr string
		if err := json.Unmarshal(event.Message, &msgStr); err == nil && msgStr != "" {
			s.output <- Output{Channel: "system", Data: []byte(msgStr)}
		}

	default:
		// Unknown event types (content_block_start, content_block_delta, etc.)
		// are intermediate streaming events. Skip silently.
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

// SetResumeSessionID pre-seeds the native session ID so the first Send()
// uses --resume to continue an existing Claude Code session.
func (s *claudeCodeSession) SetResumeSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}

// NativeSessionEntry matches the structure in Claude Code's sessions-index.json.
type NativeSessionEntry struct {
	SessionID    string `json:"sessionId"`
	Summary      string `json:"summary,omitempty"`
	FirstPrompt  string `json:"firstPrompt,omitempty"`
	MessageCount int    `json:"messageCount"`
	ProjectPath  string `json:"projectPath,omitempty"`
	GitBranch    string `json:"gitBranch,omitempty"`
	Created      string `json:"created,omitempty"`
	Modified     string `json:"modified,omitempty"`
}

// ListNativeSessions scans ~/.claude/projects/*/sessions-index.json
// and returns all discovered native Claude Code sessions.
func ListNativeSessions() ([]NativeSessionEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	projectsDir := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read projects dir: %w", err)
	}

	var allSessions []NativeSessionEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		indexPath := filepath.Join(projectsDir, entry.Name(), "sessions-index.json")
		data, err := os.ReadFile(indexPath)
		if err != nil {
			continue // skip dirs without index
		}

		var index struct {
			Entries []NativeSessionEntry `json:"entries"`
		}
		if err := json.Unmarshal(data, &index); err != nil {
			continue
		}
		allSessions = append(allSessions, index.Entries...)
	}

	// Sort by modified time, most recent first.
	sort.Slice(allSessions, func(i, j int) bool {
		return allSessions[i].Modified > allSessions[j].Modified
	})

	// Limit to 50 most recent.
	if len(allSessions) > 50 {
		allSessions = allSessions[:50]
	}

	return allSessions, nil
}
