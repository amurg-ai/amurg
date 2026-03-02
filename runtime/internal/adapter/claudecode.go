package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// ClaudeCodeAdapter implements the claude-code profile.
// It uses Claude Code's bidirectional stream-json mode for persistent,
// interactive sessions via stdin/stdout.
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

// claudeCodeSession manages a persistent Claude Code conversation.
// The process is spawned lazily on the first Send() and maintained
// across multiple messages via --input-format stream-json.
type claudeCodeSession struct {
	ctx         context.Context
	cfg         config.ClaudeCodeConfig
	security    *config.SecurityConfig
	sessionID   string // Claude Code's native session ID
	permHandler func(tool, description, resource string) bool

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output chan Output
	done   chan struct{} // closed when process exits

	mu           sync.Mutex
	started      bool
	closed       bool
	turnComplete atomic.Bool // prevents double ExitCode on result + process exit
}

// startProcess spawns the persistent Claude Code process.
// Called lazily on first Send() after SetResumeSessionID has been applied.
func (s *claudeCodeSession) startProcess() error {
	s.mu.Lock()
	if s.started {
		// Check if process is still running.
		if s.done != nil {
			select {
			case <-s.done:
				// Process exited, need restart.
				s.started = false
			default:
				// Process still running.
				s.mu.Unlock()
				return nil
			}
		}
	}
	sid := s.sessionID
	s.mu.Unlock()

	s.turnComplete.Store(false)

	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	}

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
	if sid != "" {
		args = append(args, "--resume", sid)
	}

	cmd := exec.CommandContext(s.ctx, s.cfg.Command, args...)

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

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
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

	done := make(chan struct{})

	s.mu.Lock()
	s.cmd = cmd
	s.stdin = stdin
	s.done = done
	s.started = true
	s.mu.Unlock()

	// Read stderr in background.
	go func() {
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
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			s.handleStreamEvent(line)
		}
	}()

	// Wait for process exit. Emit ExitCode only if no result event already did.
	go func() {
		_ = cmd.Wait()

		if !s.turnComplete.Load() {
			var exitCode *int
			if cmd.ProcessState != nil {
				code := cmd.ProcessState.ExitCode()
				exitCode = &code
			}
			if exitCode == nil {
				code := 1
				exitCode = &code
			}
			s.output <- Output{Channel: "system", Data: nil, ExitCode: exitCode}
		}

		close(done)
	}()

	return nil
}

func (s *claudeCodeSession) Send(ctx context.Context, input []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}

	needsStart := !s.started
	if s.started && s.done != nil {
		select {
		case <-s.done:
			needsStart = true
		default:
		}
	}
	s.mu.Unlock()

	s.turnComplete.Store(false)

	if needsStart {
		if err := s.startProcess(); err != nil {
			return err
		}
	}

	// Build the stream-json input message.
	msg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": string(input),
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal input: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin == nil {
		return fmt.Errorf("process not started")
	}
	_, err = s.stdin.Write(data)
	return err
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
		Subtype   string          `json:"subtype"`
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
	case "system":
		// Init event carries session_id and subtype "init".
		if event.Subtype == "init" {
			if event.SessionID != "" {
				s.mu.Lock()
				s.sessionID = event.SessionID
				s.mu.Unlock()
			}
			return // Don't emit init as output.
		}
		// Regular system message.
		var msgStr string
		if err := json.Unmarshal(event.Message, &msgStr); err == nil && msgStr != "" {
			s.output <- Output{Channel: "system", Data: []byte(msgStr)}
		}

	case "result":
		// Save session ID for future resume / restart.
		if event.SessionID != "" {
			s.mu.Lock()
			s.sessionID = event.SessionID
			s.mu.Unlock()
		}
		// Signal turn completion — process stays alive for next message.
		s.turnComplete.Store(true)
		code := 0
		s.output <- Output{Channel: "system", Data: nil, ExitCode: &code}

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
					// Detect interactive tools and emit on "question" channel
					// so the UI can render them as interactive elements.
					channel := "tool"
					if isInteractiveTool(c.Name) {
						channel = "question"
					}
					toolData := map[string]any{
						"type":  "tool_use",
						"id":    c.ID,
						"name":  c.Name,
						"input": json.RawMessage(c.Input),
					}
					if data, err := json.Marshal(toolData); err == nil {
						s.output <- Output{Channel: channel, Data: data}
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

// interactiveTools are Claude Code tools that require user interaction
// and cannot function in pipe mode. These get routed to the UI.
var interactiveTools = map[string]bool{
	"AskUserQuestion": true,
	"EnterPlanMode":   true,
	"ExitPlanMode":    true,
}

func isInteractiveTool(name string) bool {
	return interactiveTools[name]
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
	stdin := s.stdin
	cmd := s.cmd
	done := s.done
	s.mu.Unlock()

	// Close stdin to signal EOF — process should exit gracefully.
	if stdin != nil {
		_ = stdin.Close()
	}
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

// NativeHandle returns the Claude Code native session ID.
func (s *claudeCodeSession) NativeHandle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
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

// ListNativeSessions scans ~/.claude/projects/*/sessions-index.json
// and returns all discovered native Claude Code sessions.
// Implements NativeSessionLister interface.
func (a *ClaudeCodeAdapter) ListNativeSessions() ([]NativeSessionEntry, error) {
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
