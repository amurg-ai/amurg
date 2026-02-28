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
	"time"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// KiloAdapter implements the kilo-code profile.
// It uses Kilo Code CLI's `run --auto --json` mode,
// spawning a new process per Send() call and tracking session IDs.
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

// ListNativeSessions runs `kilo session list` or scans local session data.
func (a *KiloAdapter) ListNativeSessions() ([]NativeSessionEntry, error) {
	// Try running `kilo session list` to get sessions.
	// Fall back to scanning local config directory.
	sessions, err := listKiloSessionsFromCLI()
	if err == nil && len(sessions) > 0 {
		return sessions, nil
	}

	return listKiloSessionsFromDisk()
}

// listKiloSessionsFromCLI tries to get session list from the kilo CLI.
func listKiloSessionsFromCLI() ([]NativeSessionEntry, error) {
	cmd := exec.Command("kilo", "session", "list", "--json")
	cmd.Env = append(os.Environ(), "KILO_EPHEMERAL_MODE=true")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var sessions []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
		Messages  int    `json:"messages"`
	}
	if err := json.Unmarshal(out, &sessions); err != nil {
		return nil, err
	}

	entries := make([]NativeSessionEntry, 0, len(sessions))
	for _, s := range sessions {
		entries = append(entries, NativeSessionEntry{
			SessionID:    s.ID,
			Summary:      s.Title,
			MessageCount: s.Messages,
			Created:      s.CreatedAt,
			Modified:     s.UpdatedAt,
		})
	}

	if len(entries) > 50 {
		entries = entries[:50]
	}

	return entries, nil
}

// listKiloSessionsFromDisk scans the local Kilo config directory for sessions.
func listKiloSessionsFromDisk() ([]NativeSessionEntry, error) {
	configDir := kiloConfigDir()
	if configDir == "" {
		return nil, nil
	}

	// Kilo stores session data in the config directory.
	sessionsDir := filepath.Join(configDir, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	var sessions []NativeSessionEntry
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			sessionID := strings.TrimSuffix(entry.Name(), ".json")
			nse := NativeSessionEntry{
				SessionID: sessionID,
			}

			// Try to read session metadata.
			data, err := os.ReadFile(filepath.Join(sessionsDir, entry.Name()))
			if err == nil {
				var meta struct {
					ID        string `json:"id"`
					Title     string `json:"title"`
					CreatedAt string `json:"createdAt"`
					UpdatedAt string `json:"updatedAt"`
					Messages  int    `json:"messages"`
					Cwd       string `json:"cwd"`
				}
				if err := json.Unmarshal(data, &meta); err == nil {
					if meta.ID != "" {
						nse.SessionID = meta.ID
					}
					nse.Summary = meta.Title
					nse.MessageCount = meta.Messages
					nse.Created = meta.CreatedAt
					nse.Modified = meta.UpdatedAt
					nse.ProjectPath = meta.Cwd
				}
			}

			if nse.Modified == "" {
				if info, err := entry.Info(); err == nil {
					nse.Modified = info.ModTime().Format(time.RFC3339)
				}
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

// kiloSession manages a Kilo Code CLI conversation across multiple Send() calls.
type kiloSession struct {
	ctx       context.Context
	cfg       config.KiloConfig
	security  *config.SecurityConfig
	sessionID string // Kilo session ID for --session resume

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
	sid := s.sessionID
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

	// Model and provider via CLI flags (preferred over env vars).
	if s.cfg.Model != "" {
		args = append(args, "--model", s.cfg.Model)
	}
	if s.cfg.Provider != "" {
		args = append(args, "--provider", s.cfg.Provider)
	}

	// Operating mode.
	if s.cfg.Mode != "" {
		args = append(args, "--mode", s.cfg.Mode)
	}

	// System prompt.
	if s.cfg.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", s.cfg.SystemPrompt)
	}

	// Timeout.
	if s.cfg.Timeout > 0 {
		args = append(args, "--timeout", strconv.Itoa(s.cfg.Timeout))
	}

	// Working directory.
	if dir := resolveWorkDir(s.cfg.WorkDir, s.security); dir != "" {
		args = append(args, "--workspace", dir)
	}

	// Session continuity: use --session with session ID, fallback to --continue.
	if sid != "" {
		args = append(args, "--session", sid)
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
		Timestamp int64  `json:"timestamp,omitempty"`
		Source    string `json:"source,omitempty"`    // "cli" or "extension"
		Type      string `json:"type,omitempty"`
		Say       string `json:"say,omitempty"`       // "text", "reasoning", "tool", etc.
		ID        string `json:"id,omitempty"`
		Partial   bool   `json:"partial,omitempty"`
		Content   string `json:"content,omitempty"`
		Metadata  json.RawMessage `json:"metadata,omitempty"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		// Not valid JSON — emit as raw stdout.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
		return
	}

	// Extract session ID from welcome message metadata.
	if msg.Type == "welcome" && len(msg.Metadata) > 0 {
		var meta struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(msg.Metadata, &meta); err == nil && meta.SessionID != "" {
			s.mu.Lock()
			if s.sessionID == "" {
				s.sessionID = meta.SessionID
			}
			s.mu.Unlock()
		}
		return // Don't emit welcome messages.
	}

	// Skip partial streaming messages — wait for the complete version.
	// This avoids duplicating content that arrives incrementally.
	if msg.Partial {
		return
	}

	// Route based on the "say" field (message subtype).
	switch msg.Say {
	case "text":
		if msg.Content != "" {
			s.output <- Output{Channel: "stdout", Data: []byte(msg.Content)}
		}

	case "reasoning":
		if msg.Content != "" {
			s.output <- Output{Channel: "stdout", Data: []byte("*" + truncateStr(msg.Content, 500) + "*")}
		}

	case "tool":
		// Tool call/result — parse the content as structured tool data.
		if msg.Content != "" {
			s.handleKiloToolMessage(msg.ID, msg.Content)
		}

	case "command", "command_output":
		// Command execution and output.
		if msg.Content != "" {
			// Try to parse as structured tool data.
			toolData := map[string]any{
				"type": "tool_use",
				"id":   msg.ID,
				"name": "execute_command",
				"input": map[string]any{
					"command": msg.Content,
				},
			}
			if data, err := json.Marshal(toolData); err == nil {
				s.output <- Output{Channel: "tool", Data: data}
			}
		}

	case "completion_result":
		if msg.Content != "" {
			s.output <- Output{Channel: "stdout", Data: []byte(msg.Content)}
		}

	case "error":
		if msg.Content != "" {
			s.output <- Output{Channel: "stderr", Data: []byte(msg.Content)}
		}

	default:
		// For unrecognized say types, emit content if present.
		if msg.Content != "" {
			s.output <- Output{Channel: "stdout", Data: []byte(msg.Content)}
		} else if msg.Type != "" {
			// Emit the raw JSON for event types without textual content.
			cp := make([]byte, len(line))
			copy(cp, line)
			s.output <- Output{Channel: "stdout", Data: cp}
		}
	}
}

// handleKiloToolMessage parses tool call content and emits structured tool data.
func (s *kiloSession) handleKiloToolMessage(id, content string) {
	// Try to parse the content as JSON (tool calls are often JSON-encoded).
	var toolInfo struct {
		Tool      string          `json:"tool"`
		Path      string          `json:"path,omitempty"`
		Command   string          `json:"command,omitempty"`
		Content   string          `json:"content,omitempty"`
		Diff      string          `json:"diff,omitempty"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		Result    string          `json:"result,omitempty"`
		Status    string          `json:"status,omitempty"`
	}
	if err := json.Unmarshal([]byte(content), &toolInfo); err == nil && toolInfo.Tool != "" {
		// Structured tool call.
		input := map[string]any{}
		if toolInfo.Path != "" {
			input["path"] = toolInfo.Path
		}
		if toolInfo.Command != "" {
			input["command"] = toolInfo.Command
		}
		if len(toolInfo.Arguments) > 0 {
			input["arguments"] = json.RawMessage(toolInfo.Arguments)
		}

		toolData := map[string]any{
			"type":  "tool_use",
			"id":    id,
			"name":  toolInfo.Tool,
			"input": input,
		}
		if data, err := json.Marshal(toolData); err == nil {
			s.output <- Output{Channel: "tool", Data: data}
		}

		// Emit result if present.
		if toolInfo.Result != "" {
			resultContent := toolInfo.Result
			if len(resultContent) > maxToolResultLen {
				resultContent = resultContent[:maxToolResultLen] + "\n... (truncated)"
			}
			resultData := map[string]any{
				"type":        "tool_result",
				"tool_use_id": id,
				"content":     resultContent,
				"is_error":    toolInfo.Status == "error",
			}
			if data, err := json.Marshal(resultData); err == nil {
				s.output <- Output{Channel: "tool", Data: data}
			}
		}
		return
	}

	// Not structured — emit as plain tool output.
	toolData := map[string]any{
		"type":    "tool_use",
		"id":      id,
		"name":    "unknown",
		"input":   map[string]any{"raw": content},
	}
	if data, err := json.Marshal(toolData); err == nil {
		s.output <- Output{Channel: "tool", Data: data}
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

// NativeHandle returns the Kilo native session ID.
func (s *kiloSession) NativeHandle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// SetResumeSessionID pre-seeds the session ID so the first Send()
// uses --session to continue an existing Kilo session.
func (s *kiloSession) SetResumeSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}

// LoadNativeHistory exports and parses the Kilo session history.
func (s *kiloSession) LoadNativeHistory() []Output {
	s.mu.Lock()
	sid := s.sessionID
	s.mu.Unlock()
	if sid == "" {
		return nil
	}

	// Use `kilo export <sessionID>` to get session data.
	cmd := exec.Command("kilo", "export", sid)
	cmd.Env = append(os.Environ(), "KILO_EPHEMERAL_MODE=true")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	return parseKiloExportedHistory(out)
}

// parseKiloExportedHistory parses the JSON output from `kilo export`.
func parseKiloExportedHistory(data []byte) []Output {
	// The export format is a JSON object with a messages array.
	var export struct {
		Messages []struct {
			Type    string `json:"type"`
			Say     string `json:"say"`
			Content string `json:"content"`
			ID      string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &export); err != nil {
		// Try as raw array of messages.
		if err := json.Unmarshal(data, &export.Messages); err != nil {
			return nil
		}
	}

	var outputs []Output
	for _, msg := range export.Messages {
		switch {
		case msg.Type == "say" && msg.Say == "text" && msg.Content != "":
			// Determine if this is user or assistant based on message structure.
			// Messages from the extension/agent are assistant messages.
			outputs = append(outputs, Output{Channel: "history_assistant", Data: []byte(msg.Content)})

		case msg.Type == "say" && msg.Say == "tool" && msg.Content != "":
			toolData := map[string]any{
				"type":    "tool_use",
				"id":      msg.ID,
				"name":    "unknown",
				"input":   map[string]any{"raw": msg.Content},
			}
			if data, err := json.Marshal(toolData); err == nil {
				outputs = append(outputs, Output{Channel: "history_tool", Data: data})
			}

		case msg.Type == "say" && msg.Say == "command" && msg.Content != "":
			toolData := map[string]any{
				"type": "tool_use",
				"name": "execute_command",
				"input": map[string]any{
					"command": msg.Content,
				},
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

// kiloConfigDir returns the Kilo Code configuration directory.
func kiloConfigDir() string {
	// Check XDG_CONFIG_HOME first.
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dir := filepath.Join(xdg, "kilo")
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".config", "kilo")
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	return ""
}
