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
	"strings"
	"sync"
	"time"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// GeminiCLIAdapter implements the gemini-cli profile.
// It uses Gemini CLI's `-p --output-format stream-json` mode,
// spawning a new process per Send() call and using --resume for session continuity.
type GeminiCLIAdapter struct{}

func (a *GeminiCLIAdapter) Start(ctx context.Context, cfg config.AgentConfig) (AgentSession, error) {
	gCfg := cfg.Gemini
	if gCfg == nil {
		gCfg = &config.GeminiCLIConfig{}
	}
	if gCfg.Command == "" {
		gCfg.Command = "gemini"
	}

	sess := &geminiSession{
		ctx:      ctx,
		cfg:      *gCfg,
		security: cfg.Security,
		output:   make(chan Output, 64),
	}
	return sess, nil
}

// ListNativeSessions scans ~/.gemini/tmp/*/chats/ and returns discovered sessions.
func (a *GeminiCLIAdapter) ListNativeSessions() ([]NativeSessionEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	geminiDir := os.Getenv("GEMINI_CLI_HOME")
	if geminiDir == "" {
		geminiDir = filepath.Join(home, ".gemini")
	}

	tmpDir := filepath.Join(geminiDir, "tmp")
	projectDirs, err := os.ReadDir(tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read gemini tmp dir: %w", err)
	}

	var sessions []NativeSessionEntry
	for _, projEntry := range projectDirs {
		if !projEntry.IsDir() {
			continue
		}

		chatsDir := filepath.Join(tmpDir, projEntry.Name(), "chats")
		chatEntries, err := os.ReadDir(chatsDir)
		if err != nil {
			continue
		}

		for _, chatEntry := range chatEntries {
			if chatEntry.IsDir() {
				continue
			}
			name := chatEntry.Name()
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}

			sessionID := strings.TrimSuffix(name, ".jsonl")
			nse := NativeSessionEntry{
				SessionID: sessionID,
			}

			info, err := chatEntry.Info()
			if err == nil {
				nse.Modified = info.ModTime().Format(time.RFC3339)
			}

			// Try to read first line for metadata/prompt.
			chatPath := filepath.Join(chatsDir, name)
			if firstPrompt := readGeminiFirstPrompt(chatPath); firstPrompt != "" {
				nse.FirstPrompt = truncateStr(firstPrompt, 200)
				nse.Summary = truncateStr(firstPrompt, 100)
			}

			sessions = append(sessions, nse)
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified > sessions[j].Modified
	})

	if len(sessions) > 50 {
		sessions = sessions[:50]
	}

	return sessions, nil
}

// readGeminiFirstPrompt reads a Gemini session JSONL to find the first user prompt.
func readGeminiFirstPrompt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		var event struct {
			Type    string `json:"type"`
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
			Text    string `json:"text,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Role == "user" || event.Type == "user" {
			if event.Content != "" {
				return event.Content
			}
			if event.Text != "" {
				return event.Text
			}
		}
	}
	return ""
}

// geminiSession manages a Gemini CLI conversation across multiple Send() calls.
type geminiSession struct {
	ctx       context.Context
	cfg       config.GeminiCLIConfig
	security  *config.SecurityConfig
	sessionID string // Gemini session UUID for --resume

	output chan Output
	mu     sync.Mutex
	cmd    *exec.Cmd
	done   chan struct{}
	closed bool

	permHandler func(tool, description, resource string) bool
}

func (s *geminiSession) Send(ctx context.Context, input []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	sid := s.sessionID
	s.mu.Unlock()

	// -p takes the prompt as its value (not a boolean flag).
	args := []string{"-p", string(input), "--output-format", "stream-json"}

	// Permission mode - security config takes precedence.
	approvalMode := s.cfg.ApprovalMode
	if s.security != nil && s.security.PermissionMode != "" {
		switch s.security.PermissionMode {
		case "skip":
			approvalMode = "yolo"
		case "auto":
			approvalMode = "auto_edit"
		}
	}
	if approvalMode == "yolo" {
		args = append(args, "--yolo")
	} else if approvalMode != "" && approvalMode != "default" {
		args = append(args, "--approval-mode", approvalMode)
	}

	// Model.
	if s.cfg.Model != "" {
		args = append(args, "--model", s.cfg.Model)
	}

	// Sandbox.
	if s.cfg.Sandbox {
		args = append(args, "--sandbox")
	}

	// Include directories.
	if len(s.cfg.IncludeDirs) > 0 {
		args = append(args, "--include-directories", strings.Join(s.cfg.IncludeDirs, ","))
	}

	// Resume with native session ID.
	if sid != "" {
		args = append(args, "--resume", sid)
	}

	cmd := exec.CommandContext(ctx, s.cfg.Command, args...)

	// Working directory — validated with fallback.
	if dir := resolveWorkDir(s.cfg.WorkDir, s.security); dir != "" {
		cmd.Dir = dir
	}

	cmd.Env = os.Environ()
	for k, v := range s.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// System prompt file override.
	if s.cfg.SystemPromptFile != "" {
		cmd.Env = append(cmd.Env, "GEMINI_SYSTEM_MD="+s.cfg.SystemPromptFile)
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
		return fmt.Errorf("start gemini process: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.done = make(chan struct{})
	s.mu.Unlock()

	done := s.done

	var wg sync.WaitGroup
	wg.Add(2)

	// Read stderr in background.
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

// handleStreamEvent parses a single NDJSON line from gemini stream-json output.
func (s *geminiSession) handleStreamEvent(line []byte) {
	var event struct {
		Type      string          `json:"type"`
		SessionID string          `json:"session_id,omitempty"`
		Role      string          `json:"role,omitempty"`
		Content   string          `json:"content,omitempty"`
		Text      string          `json:"text,omitempty"`
		ToolName  string          `json:"tool_name,omitempty"`
		ToolID    string          `json:"tool_id,omitempty"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		Result    json.RawMessage `json:"result,omitempty"`
		IsError   bool            `json:"is_error,omitempty"`
		Error     json.RawMessage `json:"error,omitempty"`
		Stats     json.RawMessage `json:"stats,omitempty"`
		Response  string          `json:"response,omitempty"`
		Message   json.RawMessage `json:"message,omitempty"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
		return
	}

	switch event.Type {
	case "init":
		// Session metadata — extract session ID.
		if event.SessionID != "" {
			s.mu.Lock()
			s.sessionID = event.SessionID
			s.mu.Unlock()
		}

	case "message", "content":
		// Assistant or user message content.
		text := event.Content
		if text == "" {
			text = event.Text
		}
		if text != "" {
			s.output <- Output{Channel: "stdout", Data: []byte(text)}
		}

	case "tool_use", "tool_call_request":
		// Tool call — emit on "tool" channel.
		toolData := map[string]any{
			"type":  "tool_use",
			"id":    event.ToolID,
			"name":  event.ToolName,
			"input": json.RawMessage(event.Arguments),
		}
		if data, err := json.Marshal(toolData); err == nil {
			s.output <- Output{Channel: "tool", Data: data}
		}

	case "tool_result", "tool_call_response":
		// Tool result — emit on "tool" channel.
		var resultContent string
		if len(event.Result) > 0 {
			// Try as string first.
			if err := json.Unmarshal(event.Result, &resultContent); err != nil {
				resultContent = string(event.Result)
			}
		}
		if len(resultContent) > maxToolResultLen {
			resultContent = resultContent[:maxToolResultLen] + "\n... (truncated)"
		}
		resultData := map[string]any{
			"type":        "tool_result",
			"tool_use_id": event.ToolID,
			"content":     resultContent,
			"is_error":    event.IsError,
		}
		if data, err := json.Marshal(resultData); err == nil {
			s.output <- Output{Channel: "tool", Data: data}
		}

	case "result":
		// Final result — extract session ID and stats.
		if event.SessionID != "" {
			s.mu.Lock()
			s.sessionID = event.SessionID
			s.mu.Unlock()
		}
		// Don't emit result text — duplicates content events.
		if len(event.Stats) > 0 {
			statsData := map[string]any{
				"type":  "usage",
				"stats": json.RawMessage(event.Stats),
			}
			if data, err := json.Marshal(statsData); err == nil {
				s.output <- Output{Channel: "system", Data: data}
			}
		}

	case "error":
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stderr", Data: cp}

	default:
		// tool_call_confirmation, thinking, etc. — skip silently.
	}
}

func (s *geminiSession) Output() <-chan Output {
	return s.output
}

func (s *geminiSession) Wait() error {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}

func (s *geminiSession) Stop() error {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

func (s *geminiSession) Close() error {
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

func (s *geminiSession) ExitCode() *int {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		return &code
	}
	return nil
}

func (s *geminiSession) SetPermissionHandler(handler func(tool, description, resource string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permHandler = handler
}

// NativeHandle returns the Gemini native session ID.
func (s *geminiSession) NativeHandle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// SetResumeSessionID pre-seeds the native session ID so the first Send()
// uses --resume to continue an existing Gemini session.
func (s *geminiSession) SetResumeSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}

// LoadNativeHistory reads the Gemini session JSONL and returns
// conversation history items.
func (s *geminiSession) LoadNativeHistory() []Output {
	s.mu.Lock()
	sid := s.sessionID
	s.mu.Unlock()
	if sid == "" {
		return nil
	}

	chatPath := findGeminiSessionFile(sid)
	if chatPath == "" {
		return nil
	}

	f, err := os.Open(chatPath)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var outputs []Output
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	for scanner.Scan() {
		var event struct {
			Type     string          `json:"type"`
			Role     string          `json:"role,omitempty"`
			Content  string          `json:"content,omitempty"`
			Text     string          `json:"text,omitempty"`
			ToolName string          `json:"tool_name,omitempty"`
			ToolID   string          `json:"tool_id,omitempty"`
			Arguments json.RawMessage `json:"arguments,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}

		text := event.Content
		if text == "" {
			text = event.Text
		}

		switch {
		case event.Role == "user" || event.Type == "user":
			if text != "" {
				outputs = append(outputs, Output{Channel: "history_user", Data: []byte(text)})
			}
		case event.Role == "assistant" || event.Type == "message" || event.Type == "content":
			if text != "" {
				outputs = append(outputs, Output{Channel: "history_assistant", Data: []byte(text)})
			}
		case event.Type == "tool_use" || event.Type == "tool_call_request":
			toolData := map[string]any{
				"type":  "tool_use",
				"id":    event.ToolID,
				"name":  event.ToolName,
				"input": json.RawMessage(event.Arguments),
			}
			if data, err := json.Marshal(toolData); err == nil {
				outputs = append(outputs, Output{Channel: "history_tool", Data: data})
			}
		}
	}

	if len(outputs) > 0 {
		outputs = append(outputs, Output{Channel: "system", Data: []byte("Session history loaded. Send a message to continue.")})
	}

	return outputs
}

// findGeminiSessionFile searches for a session JSONL file by UUID across project directories.
func findGeminiSessionFile(sessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	geminiDir := os.Getenv("GEMINI_CLI_HOME")
	if geminiDir == "" {
		geminiDir = filepath.Join(home, ".gemini")
	}

	tmpDir := filepath.Join(geminiDir, "tmp")
	projectDirs, err := os.ReadDir(tmpDir)
	if err != nil {
		return ""
	}

	fileName := sessionID + ".jsonl"
	for _, projEntry := range projectDirs {
		if !projEntry.IsDir() {
			continue
		}
		candidate := filepath.Join(tmpDir, projEntry.Name(), "chats", fileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

