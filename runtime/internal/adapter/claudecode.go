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
	ctx            context.Context
	cfg            config.ClaudeCodeConfig
	security       *config.SecurityConfig
	sessionID      string // Claude Code's native session ID
	resumeExplicit bool   // true only when SetResumeSessionID was called (explicit resume)
	permHandler    func(tool, description, resource string) bool
	lastInput      []byte
	retryCount     int

	tempAllowedTools         map[string]struct{}
	processUsesTempAllowlist bool
	restartInProgress        bool

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
	// Only use --resume for explicitly resumed sessions (SetResumeSessionID).
	// Auto-captured session IDs from result events must NOT be used for
	// resume because --resume overrides the working directory with the
	// original session's project context, breaking cd-to-folder behavior.
	sid := ""
	if s.resumeExplicit {
		sid = s.sessionID
	}
	s.mu.Unlock()

	s.turnComplete.Store(false)

	s.mu.Lock()
	extraAllowedTools := claudeToolSetKeys(s.tempAllowedTools)
	s.mu.Unlock()

	args, skipPerms := buildClaudeArgs(s.cfg, s.security, sid, extraAllowedTools)

	cmd := exec.CommandContext(s.ctx, s.cfg.Command, args...)

	// Working directory — always resolved to a valid dir (home as fallback).
	cmd.Dir = resolveWorkDir(s.cfg.WorkDir, s.security)

	// Filter out env vars that trigger nested-session detection in Claude Code.
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "CLAUDECODE=") ||
			strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") {
			continue
		}
		cmd.Env = append(cmd.Env, e)
	}
	// Reinforce skip-permissions via env var in case the CLI flag alone isn't enough.
	if skipPerms {
		cmd.Env = append(cmd.Env, "CLAUDE_DANGEROUS_SKIP_PERMISSIONS=true")
	}
	for k, v := range s.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	fmt.Fprintf(os.Stderr, "claude-code: spawning %s %s (dir=%s, resume=%v)\n",
		s.cfg.Command, strings.Join(args, " "), cmd.Dir, sid != "")

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
	s.processUsesTempAllowlist = len(extraAllowedTools) > 0
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
	go func(cmd *exec.Cmd, done chan struct{}) {
		_ = cmd.Wait()

		suppressFinal := false
		s.mu.Lock()
		if s.cmd == cmd {
			s.started = false
			s.cmd = nil
			s.stdin = nil
			s.done = nil
		}
		if s.restartInProgress {
			suppressFinal = true
			s.restartInProgress = false
		}
		s.mu.Unlock()

		if !s.turnComplete.Load() && !suppressFinal {
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
	}(cmd, done)

	return nil
}

func (s *claudeCodeSession) Send(ctx context.Context, input []byte) error {
	var (
		cleanupTempAllowlist bool
		restartCmd           *exec.Cmd
		restartDone          chan struct{}
	)

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
	if s.processUsesTempAllowlist {
		cleanupTempAllowlist = true
		needsStart = true
		if s.started && s.done != nil {
			select {
			case <-s.done:
			default:
				restartCmd = s.cmd
				restartDone = s.done
				s.restartInProgress = true
			}
		}
	}
	s.lastInput = append(s.lastInput[:0], input...)
	s.retryCount = 0
	s.mu.Unlock()

	if restartCmd != nil && restartCmd.Process != nil {
		_ = restartCmd.Process.Signal(os.Interrupt)
	}
	if restartDone != nil {
		<-restartDone
	}
	if cleanupTempAllowlist {
		s.mu.Lock()
		s.tempAllowedTools = nil
		s.processUsesTempAllowlist = false
		s.mu.Unlock()
	}

	s.turnComplete.Store(false)

	if needsStart {
		if err := s.startProcess(); err != nil {
			return err
		}
	}

	return s.writeInput(input)
}

func (s *claudeCodeSession) writeInput(input []byte) error {
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

func (s *claudeCodeSession) retryWithTemporaryToolAccess(tools []string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.tempAllowedTools == nil {
		s.tempAllowedTools = make(map[string]struct{}, len(tools))
	}
	for _, tool := range tools {
		if tool = strings.TrimSpace(tool); tool != "" {
			s.tempAllowedTools[tool] = struct{}{}
		}
	}
	s.retryCount++
	input := append([]byte(nil), s.lastInput...)
	cmd := s.cmd
	done := s.done
	if cmd != nil && cmd.Process != nil {
		s.restartInProgress = true
	}
	s.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	if done != nil {
		<-done
	}

	if err := s.startProcess(); err != nil {
		s.emitRetryFailure(fmt.Errorf("restart claude after permission approval: %w", err))
		return
	}
	if err := s.writeInput(input); err != nil {
		s.emitRetryFailure(fmt.Errorf("resend claude input after permission approval: %w", err))
	}
}

func (s *claudeCodeSession) emitRetryFailure(err error) {
	msg := err.Error()
	code := 1
	s.turnComplete.Store(true)
	s.output <- Output{Channel: "stderr", Data: []byte(msg)}
	s.output <- Output{Channel: "system", Data: nil, ExitCode: &code}
}

// truncateStr limits s to maxLen characters, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func buildClaudeArgs(cfg config.ClaudeCodeConfig, security *config.SecurityConfig, resumeID string, extraAllowedTools []string) ([]string, bool) {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	}

	skipPerms := false
	permMode := cfg.PermissionMode
	if security != nil && security.PermissionMode != "" {
		permMode = security.PermissionMode
	}
	switch permMode {
	case "dangerously-skip-permissions", "skip", "bypassPermissions":
		args = append(args, "--dangerously-skip-permissions")
		skipPerms = true
	case "acceptEdits", "auto", "default", "dontAsk", "plan":
		args = append(args, "--permission-mode", permMode)
	case "", "strict":
		// Claude Code's default print-mode behavior is the conservative path.
	}

	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(cfg.MaxTurns))
	}

	allowedTools := claudeMergeUnique(cfg.AllowedTools, extraAllowedTools)
	if security != nil && len(security.AllowedTools) > 0 {
		allowedTools = claudeMergeUnique(security.AllowedTools, extraAllowedTools)
	}
	for _, tool := range allowedTools {
		args = append(args, "--allowedTools", tool)
	}

	disallowedTools := cfg.DisallowedTools
	if security != nil && len(security.DisallowedTools) > 0 {
		disallowedTools = security.DisallowedTools
	}
	disallowedTools = claudeFilterExcludedTools(disallowedTools, allowedTools)
	for _, tool := range disallowedTools {
		args = append(args, "--disallowedTools", tool)
	}

	if security != nil {
		for _, dir := range claudeMergeUnique(security.AllowedPaths) {
			args = append(args, "--add-dir", dir)
		}
	}

	if cfg.SystemPrompt != "" {
		args = append(args, "--system-prompt", cfg.SystemPrompt)
	}
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}

	return args, skipPerms
}

func claudeMergeUnique(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var merged []string
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			merged = append(merged, value)
		}
	}
	return merged
}

func claudeFilterExcludedTools(disallowed, allowed []string) []string {
	if len(disallowed) == 0 {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, tool := range allowed {
		allowedSet[tool] = struct{}{}
	}

	filtered := make([]string, 0, len(disallowed))
	for _, tool := range claudeMergeUnique(disallowed) {
		if _, ok := allowedSet[tool]; ok {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func claudeToolSetKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type claudePermissionDenial struct {
	Tool        string
	Description string
	Resource    string
}

func parseClaudePermissionDenials(raw json.RawMessage) []claudePermissionDenial {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var objects []map[string]any
	if err := json.Unmarshal(raw, &objects); err == nil {
		return parseClaudePermissionDenialObjects(objects)
	}

	var stringsOnly []string
	if err := json.Unmarshal(raw, &stringsOnly); err == nil {
		out := make([]claudePermissionDenial, 0, len(stringsOnly))
		for _, tool := range stringsOnly {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			out = append(out, claudePermissionDenial{
				Tool:        tool,
				Description: fmt.Sprintf("Claude requested permission to use %s.", tool),
			})
		}
		return out
	}

	return nil
}

func parseClaudePermissionDenialObjects(objects []map[string]any) []claudePermissionDenial {
	var denials []claudePermissionDenial
	for _, obj := range objects {
		tool := claudeFirstString(obj,
			"tool",
			"tool_name",
			"name",
			"toolName",
		)
		description := claudeFirstString(obj,
			"description",
			"message",
			"reason",
		)
		resource := claudeFirstString(obj,
			"resource",
			"path",
			"target",
		)
		if description == "" && tool != "" {
			description = fmt.Sprintf("Claude requested permission to use %s.", tool)
		}
		if tool == "" && description == "" && resource == "" {
			continue
		}
		denials = append(denials, claudePermissionDenial{
			Tool:        tool,
			Description: description,
			Resource:    resource,
		})
	}
	return denials
}

func claudeFirstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		if s, ok := value.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

// maxToolResultLen limits stored tool result content to prevent DB bloat.
const maxToolResultLen = 50000

// handleStreamEvent parses a single NDJSON line from claude stream-json output.
func (s *claudeCodeSession) handleStreamEvent(line []byte) {
	var event struct {
		Type              string          `json:"type"`
		Subtype           string          `json:"subtype"`
		SessionID         string          `json:"session_id"`
		Result            string          `json:"result"`
		Message           json.RawMessage `json:"message"`
		PermissionDenials json.RawMessage `json:"permission_denials"`
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
		denials := parseClaudePermissionDenials(event.PermissionDenials)
		if len(denials) > 0 && s.maybeRetryWithPermissions(denials) {
			return
		}
		if len(denials) > 0 && event.Result != "" {
			s.output <- Output{Channel: "stdout", Data: []byte(event.Result)}
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

func (s *claudeCodeSession) maybeRetryWithPermissions(denials []claudePermissionDenial) bool {
	s.mu.Lock()
	handler := s.permHandler
	retryCount := s.retryCount
	s.mu.Unlock()

	if handler == nil || retryCount > 0 {
		return false
	}

	uniq := make([]claudePermissionDenial, 0, len(denials))
	seen := make(map[string]struct{}, len(denials))
	for _, denial := range denials {
		tool := strings.TrimSpace(denial.Tool)
		if tool == "" {
			return false
		}
		key := tool + "\x00" + strings.TrimSpace(denial.Resource)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if denial.Description == "" {
			denial.Description = fmt.Sprintf("Claude requested permission to use %s.", tool)
		}
		uniq = append(uniq, denial)
	}
	if len(uniq) == 0 {
		return false
	}

	approvedTools := make([]string, 0, len(uniq))
	for _, denial := range uniq {
		if !handler(denial.Tool, denial.Description, denial.Resource) {
			return false
		}
		approvedTools = append(approvedTools, denial.Tool)
	}

	s.output <- Output{
		Channel: "system",
		Data:    []byte("Permission approved. Retrying with temporary tool access."),
	}
	go s.retryWithTemporaryToolAccess(approvedTools)
	return true
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

// UpdateSecurity updates the security config. Returns true because Claude Code
// needs a process restart for new CLI flags to take effect.
func (s *claudeCodeSession) UpdateSecurity(security *config.SecurityConfig) (restartRequired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.security = security
	return true
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
	s.resumeExplicit = true
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
// and also discovers JSONL files not in the index.
// Only returns sessions whose JSONL files actually exist.
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

	seen := make(map[string]bool)
	var allSessions []NativeSessionEntry

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projDir := filepath.Join(projectsDir, entry.Name())

		// Read the index if it exists.
		indexPath := filepath.Join(projDir, "sessions-index.json")
		if data, err := os.ReadFile(indexPath); err == nil {
			var index struct {
				Entries []NativeSessionEntry `json:"entries"`
			}
			if json.Unmarshal(data, &index) == nil {
				for _, e := range index.Entries {
					// Verify the JSONL file actually exists.
					jsonlPath := e.FullPath
					if jsonlPath == "" {
						jsonlPath = filepath.Join(projDir, e.SessionID+".jsonl")
					}
					if _, err := os.Stat(jsonlPath); err != nil {
						continue // skip stale entries
					}
					seen[e.SessionID] = true
					allSessions = append(allSessions, e)
				}
			}
		}

		// Discover JSONL files not in the index.
		files, _ := filepath.Glob(filepath.Join(projDir, "*.jsonl"))
		for _, f := range files {
			base := filepath.Base(f)
			sid := strings.TrimSuffix(base, ".jsonl")
			if seen[sid] {
				continue
			}
			seen[sid] = true

			info, err := os.Stat(f)
			if err != nil {
				continue
			}

			nse := NativeSessionEntry{
				SessionID:   sid,
				FullPath:    f,
				ProjectPath: decodeProjectPath(entry.Name()),
				Modified:    info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z"),
			}

			// Try to extract first user prompt and message count.
			nse.FirstPrompt, nse.MessageCount = scanJSONLMetadata(f)
			allSessions = append(allSessions, nse)
		}
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

// decodeProjectPath converts a Claude project dir name back to a path.
// e.g. "-home-ciprian-source-amurg" -> "/home/ciprian/source/amurg"
func decodeProjectPath(dirName string) string {
	if dirName == "" {
		return ""
	}
	return "/" + strings.ReplaceAll(dirName[1:], "-", "/")
}

// scanJSONLMetadata reads a JSONL file to extract the first user prompt and message count.
func scanJSONLMetadata(path string) (firstPrompt string, messageCount int) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 512*1024)
	for scanner.Scan() {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		if entry.Type == "user" || entry.Type == "assistant" {
			messageCount++
		}
		if entry.Type == "user" && firstPrompt == "" {
			var text string
			if json.Unmarshal(entry.Message.Content, &text) == nil && text != "" {
				firstPrompt = text
				if len(firstPrompt) > 120 {
					firstPrompt = firstPrompt[:120]
				}
				continue
			}
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(entry.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "text" && b.Text != "" {
						firstPrompt = b.Text
						if len(firstPrompt) > 120 {
							firstPrompt = firstPrompt[:120]
						}
						break
					}
				}
			}
		}
	}
	return firstPrompt, messageCount
}
