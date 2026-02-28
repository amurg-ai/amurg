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

// CodexAdapter implements the codex profile.
// It uses Codex CLI's `exec --json` mode for JSONL streaming,
// spawning a new process per Send() call and using thread IDs for resume.
type CodexAdapter struct{}

func (a *CodexAdapter) Start(ctx context.Context, cfg config.AgentConfig) (AgentSession, error) {
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

// ListNativeSessions scans $CODEX_HOME/sessions/ and returns discovered sessions.
func (a *CodexAdapter) ListNativeSessions() ([]NativeSessionEntry, error) {
	sessionsDir := codexSessionsDir()
	if sessionsDir == "" {
		return nil, nil
	}

	var sessions []NativeSessionEntry

	// Walk YYYY/MM/DD/rollout-*.jsonl structure.
	err := filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}

		// Parse the first line (metadata header) of the rollout file.
		nse := parseCodexRolloutHeader(path, info)
		if nse.SessionID != "" {
			sessions = append(sessions, nse)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk sessions dir: %w", err)
	}

	// Sort by modified time, most recent first.
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified > sessions[j].Modified
	})

	if len(sessions) > 50 {
		sessions = sessions[:50]
	}

	return sessions, nil
}

// parseCodexRolloutHeader reads the first line of a rollout JSONL file
// to extract session metadata.
func parseCodexRolloutHeader(path string, info os.FileInfo) NativeSessionEntry {
	nse := NativeSessionEntry{
		Modified: info.ModTime().Format(time.RFC3339),
	}

	f, err := os.Open(path)
	if err != nil {
		return nse
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	if !scanner.Scan() {
		return nse
	}

	var header struct {
		SessionID     string `json:"session_id"`
		ThreadID      string `json:"thread_id"`
		Source        string `json:"source"`
		Timestamp     string `json:"timestamp"`
		Model         string `json:"model"`
		ModelProvider string `json:"model_provider"`
		Cwd           string `json:"cwd"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &header); err == nil {
		if header.ThreadID != "" {
			nse.SessionID = header.ThreadID
		} else if header.SessionID != "" {
			nse.SessionID = header.SessionID
		}
		nse.Created = header.Timestamp
		nse.ProjectPath = header.Cwd
	}

	// Try to extract first prompt from subsequent lines.
	for scanner.Scan() {
		var event struct {
			Type string          `json:"type"`
			Item json.RawMessage `json:"item,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Type == "item.completed" || event.Type == "item.started" {
			var item struct {
				Type    string `json:"type"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content,omitempty"`
			}
			if err := json.Unmarshal(event.Item, &item); err == nil && item.Type == "user_message" {
				for _, c := range item.Content {
					if c.Type == "text" && c.Text != "" {
						nse.FirstPrompt = truncateStr(c.Text, 200)
						nse.Summary = truncateStr(c.Text, 100)
						return nse
					}
				}
			}
		}
		// Don't scan too many lines for the first prompt.
		if nse.FirstPrompt != "" {
			break
		}
	}

	return nse
}

// codexSession manages a Codex CLI conversation across multiple Send() calls.
type codexSession struct {
	ctx      context.Context
	cfg      config.CodexConfig
	security *config.SecurityConfig
	threadID string // Codex thread ID for resume

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
	tid := s.threadID
	s.mu.Unlock()

	// Build command: codex exec --json [flags] "prompt"
	// or: codex exec resume <thread_id> --json [flags] "prompt"
	args := []string{"exec"}

	// Resume with thread ID if available.
	if tid != "" {
		args = append(args, "resume", tid)
	}

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

	// Full-auto convenience preset.
	if s.cfg.FullAuto && sandboxMode == "" && permMode == "" {
		args = append(args, "--full-auto")
	}

	// Model.
	if s.cfg.Model != "" {
		args = append(args, "--model", s.cfg.Model)
	}

	// Config profile.
	if s.cfg.Profile != "" {
		args = append(args, "--profile", s.cfg.Profile)
	}

	// Working directory — validated with fallback.
	if dir := resolveWorkDir(s.cfg.WorkDir, s.security); dir != "" {
		args = append(args, "--cd", dir)
	}

	// Additional writable directories.
	for _, dir := range s.cfg.AdditionalDirs {
		args = append(args, "--add-dir", dir)
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
		Type     string          `json:"type"`
		ThreadID string          `json:"thread_id,omitempty"`
		Item     json.RawMessage `json:"item,omitempty"`
		Usage    json.RawMessage `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		// Not valid JSON — emit as raw stdout.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
		return
	}

	switch event.Type {
	case "thread.started":
		// Extract and cache thread ID for session resume.
		if event.ThreadID != "" {
			s.mu.Lock()
			s.threadID = event.ThreadID
			s.mu.Unlock()
		}

	case "turn.completed":
		// Token usage metadata — emit on system channel for observability.
		if len(event.Usage) > 0 {
			usageData := map[string]any{
				"type":  "usage",
				"usage": json.RawMessage(event.Usage),
			}
			if data, err := json.Marshal(usageData); err == nil {
				s.output <- Output{Channel: "system", Data: data}
			}
		}

	case "item.completed", "item.updated":
		s.handleCodexItem(event.Item)

	case "item.started":
		// For streaming: emit item type for UI status indication.
		var item struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(event.Item, &item); err == nil {
			switch item.Type {
			case "command_execution", "file_change", "mcp_tool_call", "web_search":
				// Emit tool start on the tool channel.
				startData := map[string]any{
					"type":    "tool_start",
					"id":      item.ID,
					"subtype": item.Type,
				}
				if data, err := json.Marshal(startData); err == nil {
					s.output <- Output{Channel: "tool", Data: data}
				}
			}
		}

	case "error", "turn.failed":
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stderr", Data: cp}

	default:
		// Streaming delta events for real-time feedback.
		if strings.HasPrefix(event.Type, "item/") {
			s.handleCodexDelta(event.Type, line)
		}
	}
}

// handleCodexItem processes a completed or updated item from Codex.
func (s *codexSession) handleCodexItem(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}

	var item struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Text    string          `json:"text,omitempty"`
		Phase   string          `json:"phase,omitempty"` // "commentary" or "final_answer"
		Summary string          `json:"summary,omitempty"`
		Content json.RawMessage `json:"content,omitempty"`

		// command_execution fields
		Command         string `json:"command,omitempty"`
		Cwd             string `json:"cwd,omitempty"`
		Status          string `json:"status,omitempty"`
		ExitCode        *int   `json:"exitCode,omitempty"`
		DurationMs      int    `json:"durationMs,omitempty"`
		AggregatedOutput string `json:"aggregatedOutput,omitempty"`

		// file_change fields
		Changes []struct {
			Path string `json:"path"`
			Kind string `json:"kind"` // "edit", "create", "delete"
			Diff string `json:"diff"`
		} `json:"changes,omitempty"`

		// mcp_tool_call fields
		Server    string          `json:"server,omitempty"`
		Tool      string          `json:"tool,omitempty"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		Result    json.RawMessage `json:"result,omitempty"`
		Error     string          `json:"error,omitempty"`

		// web_search fields
		Query  string `json:"query,omitempty"`
		Action string `json:"action,omitempty"` // "search", "openPage", "findInPage"
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return
	}

	switch item.Type {
	case "agent_message", "message":
		// Text content.
		if item.Text != "" {
			s.output <- Output{Channel: "stdout", Data: []byte(item.Text)}
		}
		// Also check content array for multi-block messages.
		var contentBlocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(item.Content, &contentBlocks); err == nil {
			for _, c := range contentBlocks {
				if c.Type == "text" && c.Text != "" {
					s.output <- Output{Channel: "stdout", Data: []byte(c.Text)}
				}
			}
		}

	case "reasoning":
		// Reasoning/thinking output.
		if item.Summary != "" {
			s.output <- Output{Channel: "stdout", Data: []byte("*" + truncateStr(item.Summary, 500) + "*")}
		}

	case "command_execution":
		// Emit structured tool call on the "tool" channel.
		toolData := map[string]any{
			"type": "tool_use",
			"id":   item.ID,
			"name": "command_execution",
			"input": map[string]any{
				"command": item.Command,
				"cwd":     item.Cwd,
			},
		}
		if data, err := json.Marshal(toolData); err == nil {
			s.output <- Output{Channel: "tool", Data: data}
		}

		// Emit tool result.
		resultContent := item.AggregatedOutput
		if len(resultContent) > maxToolResultLen {
			resultContent = resultContent[:maxToolResultLen] + "\n... (truncated)"
		}
		resultData := map[string]any{
			"type":        "tool_result",
			"tool_use_id": item.ID,
			"content":     resultContent,
			"is_error":    item.ExitCode != nil && *item.ExitCode != 0,
		}
		if data, err := json.Marshal(resultData); err == nil {
			s.output <- Output{Channel: "tool", Data: data}
		}

	case "file_change":
		// Emit structured file changes on the "tool" channel.
		for _, change := range item.Changes {
			toolData := map[string]any{
				"type": "tool_use",
				"id":   item.ID,
				"name": "file_change",
				"input": map[string]any{
					"path": change.Path,
					"kind": change.Kind,
				},
			}
			if data, err := json.Marshal(toolData); err == nil {
				s.output <- Output{Channel: "tool", Data: data}
			}

			diffContent := change.Diff
			if len(diffContent) > maxToolResultLen {
				diffContent = diffContent[:maxToolResultLen] + "\n... (truncated)"
			}
			resultData := map[string]any{
				"type":        "tool_result",
				"tool_use_id": item.ID,
				"content":     diffContent,
				"is_error":    false,
			}
			if data, err := json.Marshal(resultData); err == nil {
				s.output <- Output{Channel: "tool", Data: data}
			}
		}

	case "mcp_tool_call":
		toolData := map[string]any{
			"type": "tool_use",
			"id":   item.ID,
			"name": item.Server + "/" + item.Tool,
			"input": json.RawMessage(item.Arguments),
		}
		if data, err := json.Marshal(toolData); err == nil {
			s.output <- Output{Channel: "tool", Data: data}
		}

		var resultContent string
		if item.Error != "" {
			resultContent = item.Error
		} else if len(item.Result) > 0 {
			resultContent = string(item.Result)
		}
		if len(resultContent) > maxToolResultLen {
			resultContent = resultContent[:maxToolResultLen] + "\n... (truncated)"
		}
		resultData := map[string]any{
			"type":        "tool_result",
			"tool_use_id": item.ID,
			"content":     resultContent,
			"is_error":    item.Error != "",
		}
		if data, err := json.Marshal(resultData); err == nil {
			s.output <- Output{Channel: "tool", Data: data}
		}

	case "web_search":
		toolData := map[string]any{
			"type": "tool_use",
			"id":   item.ID,
			"name": "web_search",
			"input": map[string]any{
				"query":  item.Query,
				"action": item.Action,
			},
		}
		if data, err := json.Marshal(toolData); err == nil {
			s.output <- Output{Channel: "tool", Data: data}
		}

	case "plan":
		if item.Text != "" {
			s.output <- Output{Channel: "stdout", Data: []byte(item.Text)}
		}

	case "user_message":
		// Skip — user messages in the output are echoes.
	}
}

// handleCodexDelta processes streaming delta events for real-time feedback.
func (s *codexSession) handleCodexDelta(eventType string, line []byte) {
	var delta struct {
		Delta        string `json:"delta,omitempty"`
		DeltaContent string `json:"deltaContent,omitempty"`
		Text         string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(line, &delta); err != nil {
		return
	}

	text := delta.Delta
	if text == "" {
		text = delta.DeltaContent
	}
	if text == "" {
		text = delta.Text
	}
	if text == "" {
		return
	}

	switch {
	case strings.Contains(eventType, "agentMessage"):
		s.output <- Output{Channel: "stdout", Data: []byte(text)}
	case strings.Contains(eventType, "commandExecution"):
		s.output <- Output{Channel: "stdout", Data: []byte(text)}
	case strings.Contains(eventType, "reasoning"):
		// Skip streaming reasoning — we emit the summary on completion.
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
		_ = cmd.Process.Kill()
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

// NativeHandle returns the Codex thread ID.
func (s *codexSession) NativeHandle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

// SetResumeSessionID pre-seeds the thread ID so the first Send()
// uses resume <thread_id> to continue an existing Codex session.
func (s *codexSession) SetResumeSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threadID = id
}

// LoadNativeHistory reads the Codex session rollout JSONL and returns
// conversation history items.
func (s *codexSession) LoadNativeHistory() []Output {
	s.mu.Lock()
	tid := s.threadID
	s.mu.Unlock()
	if tid == "" {
		return nil
	}

	rolloutPath := findCodexRolloutByThreadID(tid)
	if rolloutPath == "" {
		return nil
	}

	f, err := os.Open(rolloutPath)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var outputs []Output
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	for scanner.Scan() {
		var event struct {
			Type string          `json:"type"`
			Item json.RawMessage `json:"item,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}

		if event.Type != "item.completed" {
			continue
		}

		var item struct {
			Type    string `json:"type"`
			Text    string `json:"text,omitempty"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content,omitempty"`
			Command string `json:"command,omitempty"`
			Changes []struct {
				Path string `json:"path"`
				Kind string `json:"kind"`
			} `json:"changes,omitempty"`
		}
		if err := json.Unmarshal(event.Item, &item); err != nil {
			continue
		}

		switch item.Type {
		case "user_message":
			for _, c := range item.Content {
				if c.Type == "text" && c.Text != "" {
					outputs = append(outputs, Output{Channel: "history_user", Data: []byte(c.Text)})
				}
			}
		case "agent_message", "message":
			if item.Text != "" {
				outputs = append(outputs, Output{Channel: "history_assistant", Data: []byte(item.Text)})
			}
			for _, c := range item.Content {
				if c.Type == "text" && c.Text != "" {
					outputs = append(outputs, Output{Channel: "history_assistant", Data: []byte(c.Text)})
				}
			}
		case "command_execution":
			if item.Command != "" {
				toolData := map[string]any{
					"type": "tool_use",
					"name": "command_execution",
					"input": map[string]any{
						"command": item.Command,
					},
				}
				if data, err := json.Marshal(toolData); err == nil {
					outputs = append(outputs, Output{Channel: "history_tool", Data: data})
				}
			}
		case "file_change":
			for _, ch := range item.Changes {
				toolData := map[string]any{
					"type": "tool_use",
					"name": "file_change",
					"input": map[string]any{
						"path": ch.Path,
						"kind": ch.Kind,
					},
				}
				if data, err := json.Marshal(toolData); err == nil {
					outputs = append(outputs, Output{Channel: "history_tool", Data: data})
				}
			}
		}
	}

	if len(outputs) > 0 {
		outputs = append(outputs, Output{Channel: "system", Data: []byte("Session history loaded. Send a message to continue.")})
	}

	return outputs
}

// codexHomeDir returns the Codex home directory.
func codexHomeDir() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// codexSessionsDir returns the Codex sessions directory.
func codexSessionsDir() string {
	homeDir := codexHomeDir()
	if homeDir == "" {
		return ""
	}
	dir := filepath.Join(homeDir, "sessions")
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// findCodexRolloutByThreadID searches for a rollout file matching the given thread ID.
func findCodexRolloutByThreadID(threadID string) string {
	sessionsDir := codexSessionsDir()
	if sessionsDir == "" {
		return ""
	}

	var found string
	_ = filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer func() { _ = f.Close() }()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		if !scanner.Scan() {
			return nil
		}

		var header struct {
			ThreadID  string `json:"thread_id"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &header); err == nil {
			if header.ThreadID == threadID || header.SessionID == threadID {
				found = path
				return filepath.SkipAll
			}
		}
		return nil
	})

	return found
}
