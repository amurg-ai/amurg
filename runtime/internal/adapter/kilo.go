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
	cmd := exec.Command("kilo", "session", "list", "--format", "json", "-n", "50")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var sessions []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Created   int64  `json:"created"`   // unix millis
		Updated   int64  `json:"updated"`   // unix millis
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(out, &sessions); err != nil {
		return nil, err
	}

	entries := make([]NativeSessionEntry, 0, len(sessions))
	for _, s := range sessions {
		nse := NativeSessionEntry{
			SessionID:   s.ID,
			Summary:     s.Title,
			ProjectPath: s.Directory,
		}
		if s.Created > 0 {
			nse.Created = time.UnixMilli(s.Created).Format(time.RFC3339)
		}
		if s.Updated > 0 {
			nse.Modified = time.UnixMilli(s.Updated).Format(time.RFC3339)
		}
		entries = append(entries, nse)
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

	args := []string{"run", "--auto", "--format", "json"}

	// Model in provider/model format (e.g. "anthropic/claude-sonnet-4").
	if s.cfg.Model != "" {
		model := s.cfg.Model
		// If provider is set separately, combine into provider/model format.
		if s.cfg.Provider != "" && !strings.Contains(model, "/") {
			model = s.cfg.Provider + "/" + model
		}
		args = append(args, "--model", model)
	}

	// Agent mode (maps to --agent flag: "code", "architect", "ask", etc.).
	if s.cfg.Mode != "" {
		args = append(args, "--agent", s.cfg.Mode)
	}

	// Working directory.
	if dir := resolveWorkDir(s.cfg.WorkDir, s.security); dir != "" {
		args = append(args, "--dir", dir)
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

// handleKiloMessage parses a single NDJSON event from kilo run --format json.
// The format emits events: step_start, tool_use, text, step_finish.
func (s *kiloSession) handleKiloMessage(line []byte) {
	var event struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID,omitempty"`
		Part      struct {
			ID        string          `json:"id"`
			SessionID string          `json:"sessionID"`
			MessageID string          `json:"messageID"`
			Type      string          `json:"type"` // "step-start", "step-finish", "tool", "text"
			Text      string          `json:"text,omitempty"`
			Reason    string          `json:"reason,omitempty"` // "stop", "tool-calls"
			CallID    string          `json:"callID,omitempty"`
			Tool      string          `json:"tool,omitempty"`
			State     json.RawMessage `json:"state,omitempty"`
			Metadata  json.RawMessage `json:"metadata,omitempty"`
			Cost      float64         `json:"cost,omitempty"`
			Tokens    json.RawMessage `json:"tokens,omitempty"`
		} `json:"part"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		// Not valid JSON — emit as raw stdout.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stdout", Data: cp}
		return
	}

	// Extract session ID from any event that carries it.
	if event.SessionID != "" {
		s.mu.Lock()
		if s.sessionID == "" || s.sessionID != event.SessionID {
			s.sessionID = event.SessionID
		}
		s.mu.Unlock()
	} else if event.Part.SessionID != "" {
		s.mu.Lock()
		if s.sessionID == "" {
			s.sessionID = event.Part.SessionID
		}
		s.mu.Unlock()
	}

	switch event.Type {
	case "text":
		// Agent text response.
		if event.Part.Text != "" {
			s.output <- Output{Channel: "stdout", Data: []byte(event.Part.Text)}
		}

	case "tool_use":
		// Tool call with state containing input/output/status.
		s.handleKiloToolEvent(event.Part.CallID, event.Part.Tool, event.Part.State, event.Part.Metadata)

	case "step_start":
		// Step beginning — skip silently.

	case "step_finish":
		// Step completion — emit token usage on system channel for observability.
		if len(event.Part.Tokens) > 0 {
			usageData := map[string]any{
				"type":   "usage",
				"tokens": json.RawMessage(event.Part.Tokens),
				"cost":   event.Part.Cost,
			}
			if data, err := json.Marshal(usageData); err == nil {
				s.output <- Output{Channel: "system", Data: data}
			}
		}

	case "error":
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: "stderr", Data: cp}

	default:
		// Unknown event types — skip silently.
	}
}

// handleKiloToolEvent processes a tool_use event from kilo --format json.
// The state field contains status, input, output, title, and metadata.
func (s *kiloSession) handleKiloToolEvent(callID, toolName string, state, metadata json.RawMessage) {
	var toolState struct {
		Status string          `json:"status"` // "running", "completed", "error"
		Input  json.RawMessage `json:"input,omitempty"`
		Output string          `json:"output,omitempty"`
		Title  string          `json:"title,omitempty"`
		Metadata struct {
			Output      string `json:"output,omitempty"`
			Exit        int    `json:"exit"`
			Description string `json:"description,omitempty"`
			Truncated   bool   `json:"truncated,omitempty"`
		} `json:"metadata"`
		Time struct {
			Start int64 `json:"start,omitempty"`
			End   int64 `json:"end,omitempty"`
		} `json:"time"`
	}
	if len(state) > 0 {
		_ = json.Unmarshal(state, &toolState)
	}

	// Build input from the state.
	input := toolState.Input
	if len(input) == 0 {
		// Fall back to empty object.
		input = json.RawMessage(`{}`)
	}

	// Emit structured tool_use on the "tool" channel.
	toolData := map[string]any{
		"type":  "tool_use",
		"id":    callID,
		"name":  toolName,
		"input": json.RawMessage(input),
	}
	if data, err := json.Marshal(toolData); err == nil {
		s.output <- Output{Channel: "tool", Data: data}
	}

	// Emit tool_result if the tool has completed with output.
	if toolState.Status == "completed" || toolState.Output != "" || toolState.Metadata.Output != "" {
		resultContent := toolState.Output
		if resultContent == "" {
			resultContent = toolState.Metadata.Output
		}
		if len(resultContent) > maxToolResultLen {
			resultContent = resultContent[:maxToolResultLen] + "\n... (truncated)"
		}
		isError := toolState.Status == "error" || toolState.Metadata.Exit != 0
		resultData := map[string]any{
			"type":        "tool_result",
			"tool_use_id": callID,
			"content":     resultContent,
			"is_error":    isError,
		}
		if data, err := json.Marshal(resultData); err == nil {
			s.output <- Output{Channel: "tool", Data: data}
		}
	}

	// Extract reasoning from event metadata (openrouter reasoning_details).
	if len(metadata) > 0 {
		var meta struct {
			Openrouter struct {
				ReasoningDetails []struct {
					Text string `json:"text"`
				} `json:"reasoning_details,omitempty"`
			} `json:"openrouter"`
		}
		if err := json.Unmarshal(metadata, &meta); err == nil {
			for _, r := range meta.Openrouter.ReasoningDetails {
				if r.Text != "" {
					s.output <- Output{Channel: "stdout", Data: []byte("*" + truncateStr(r.Text, 500) + "*")}
				}
			}
		}
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

// UpdateSecurity updates the security config. Returns false because the next
// Send() call spawns a new process that picks up the updated config.
func (s *kiloSession) UpdateSecurity(security *config.SecurityConfig) (restartRequired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.security = security
	return false
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
// The format has an info object and a messages array with role + parts.
func parseKiloExportedHistory(data []byte) []Output {
	var export struct {
		Messages []struct {
			Info struct {
				Role string `json:"role"`
			} `json:"info"`
			Parts []struct {
				Type   string          `json:"type"` // "text", "tool", "reasoning", "step-start", "step-finish"
				Text   string          `json:"text,omitempty"`
				ID     string          `json:"id,omitempty"`
				CallID string          `json:"callID,omitempty"`
				Tool   string          `json:"tool,omitempty"`
				State  json.RawMessage `json:"state,omitempty"`
			} `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &export); err != nil {
		return nil
	}

	var outputs []Output
	for _, msg := range export.Messages {
		role := msg.Info.Role
		for _, part := range msg.Parts {
			switch {
			case role == "user" && part.Type == "text" && part.Text != "":
				outputs = append(outputs, Output{Channel: "history_user", Data: []byte(part.Text)})

			case role == "assistant" && part.Type == "text" && part.Text != "":
				outputs = append(outputs, Output{Channel: "history_assistant", Data: []byte(part.Text)})

			case part.Type == "tool" && part.Tool != "":
				input := part.State
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				toolData := map[string]any{
					"type":  "tool_use",
					"id":    part.CallID,
					"name":  part.Tool,
					"input": json.RawMessage(input),
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
