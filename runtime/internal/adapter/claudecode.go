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
	"strings"
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
	// Filter out CLAUDECODE env var to prevent nested-session detection.
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
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

// maxToolResultLen limits stored tool result content to prevent DB bloat.
const maxToolResultLen = 50000

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
		// Don't emit result text — it duplicates the assistant event content.

	case "assistant":
		// Parse message.content[] blocks.
		var msg struct {
			Content []struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
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
					// Emit structured tool call on "tool" channel.
					toolData := map[string]any{
						"type":  "tool_use",
						"id":    c.ID,
						"name":  c.Name,
						"input": json.RawMessage(c.Input),
					}
					if data, err := json.Marshal(toolData); err == nil {
						s.output <- Output{Channel: "tool", Data: data}
					}
				case "thinking":
					if c.Text != "" {
						s.output <- Output{Channel: "stdout", Data: []byte("*" + truncateStr(c.Text, 500) + "*")}
					}
				// server_tool_use, etc. — skip silently
				}
			}
		}

	case "user":
		// User events contain tool_result blocks. Emit them on the "tool" channel
		// so the UI can pair them with tool_use messages.
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(event.Message, &msg); err != nil {
			return
		}
		// content can be a string (plain prompt) or array of content blocks.
		var blocks []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		}
		if err := json.Unmarshal(msg.Content, &blocks); err != nil {
			return // string content = user prompt, skip
		}
		for _, b := range blocks {
			if b.Type != "tool_result" {
				continue
			}
			contentStr := extractToolResultText(b.Content)
			if len(contentStr) > maxToolResultLen {
				contentStr = contentStr[:maxToolResultLen] + "\n... (truncated)"
			}
			resultData := map[string]any{
				"type":        "tool_result",
				"tool_use_id": b.ToolUseID,
				"content":     contentStr,
				"is_error":    b.IsError,
			}
			if data, err := json.Marshal(resultData); err == nil {
				s.output <- Output{Channel: "tool", Data: data}
			}
		}

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

// extractToolResultText extracts plain text from a tool_result content field,
// which can be a string or an array of content blocks.
func extractToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try as string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try as array of content blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var texts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				texts = append(texts, b.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return string(raw)
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

// LoadNativeHistory reads the native Claude Code session JSONL and emits
// conversation history through the output channel. This pre-populates the
// UI when resuming an existing session.
func (s *claudeCodeSession) LoadNativeHistory() []Output {
	s.mu.Lock()
	sid := s.sessionID
	s.mu.Unlock()
	if sid == "" {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	// Find the JSONL file across all project directories.
	projectsDir := filepath.Join(home, ".claude", "projects")
	dirEntries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	var jsonlPath string
	for _, de := range dirEntries {
		if !de.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsDir, de.Name(), sid+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			jsonlPath = candidate
			break
		}
	}
	if jsonlPath == "" {
		return nil
	}

	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var history []Output
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var entry struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		switch entry.Type {
		case "user":
			// Extract user prompt text.
			var msg struct {
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(entry.Message, &msg); err != nil {
				continue
			}
			// Content can be a string or array of content blocks.
			var textContent string
			if err := json.Unmarshal(msg.Content, &textContent); err == nil {
				if textContent != "" {
					history = append(history, Output{Channel: "history_user", Data: []byte(textContent)})
				}
				continue
			}
			var blocks []struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				ToolUseID string `json:"tool_use_id"`
			}
			if err := json.Unmarshal(msg.Content, &blocks); err == nil {
				for _, b := range blocks {
					if b.Type == "text" && b.Text != "" {
						history = append(history, Output{Channel: "history_user", Data: []byte(b.Text)})
					}
					// Skip tool_result blocks in history — too verbose
				}
			}

		case "assistant":
			var msg struct {
				Content []struct {
					Type  string          `json:"type"`
					ID    string          `json:"id"`
					Text  string          `json:"text"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
			}
			if err := json.Unmarshal(entry.Message, &msg); err != nil {
				continue
			}
			for _, c := range msg.Content {
				switch c.Type {
				case "text":
					if c.Text != "" {
						history = append(history, Output{Channel: "history_assistant", Data: []byte(c.Text)})
					}
				case "tool_use":
					toolData := map[string]any{
						"type":  "tool_use",
						"id":    c.ID,
						"name":  c.Name,
						"input": json.RawMessage(c.Input),
					}
					if data, err := json.Marshal(toolData); err == nil {
						history = append(history, Output{Channel: "history_tool", Data: data})
					}
				}
			}
		}
	}

	return history
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
