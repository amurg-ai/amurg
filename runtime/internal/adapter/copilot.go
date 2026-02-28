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
	"time"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// GitHubCopilotAdapter implements the github-copilot profile.
// It uses the Copilot CLI's `-p --silent` mode,
// spawning a new process per Send() call and using --resume for session continuity.
type GitHubCopilotAdapter struct{}

func (a *GitHubCopilotAdapter) Start(ctx context.Context, cfg config.AgentConfig) (AgentSession, error) {
	copCfg := cfg.Copilot
	if copCfg == nil {
		copCfg = &config.CopilotConfig{}
	}
	if copCfg.Command == "" {
		copCfg.Command = "copilot"
	}

	sess := &copilotSession{
		ctx:      ctx,
		cfg:      *copCfg,
		security: cfg.Security,
		output:   make(chan Output, 64),
	}
	return sess, nil
}

// ListNativeSessions scans ~/.copilot/session-state/ and returns discovered sessions.
func (a *GitHubCopilotAdapter) ListNativeSessions() ([]NativeSessionEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	sessionDir := filepath.Join(home, ".copilot", "session-state")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session-state dir: %w", err)
	}

	var sessions []NativeSessionEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		nse := NativeSessionEntry{
			SessionID: sessionID,
		}

		// Try to read session metadata.
		metaPath := filepath.Join(sessionDir, sessionID, "metadata.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				DisplayName  string `json:"displayName"`
				Model        string `json:"model"`
				MessageCount int    `json:"messageCount"`
				CreatedAt    string `json:"createdAt"`
				UpdatedAt    string `json:"updatedAt"`
				Cwd          string `json:"cwd"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				nse.Summary = meta.DisplayName
				nse.MessageCount = meta.MessageCount
				nse.ProjectPath = meta.Cwd
				nse.Created = meta.CreatedAt
				nse.Modified = meta.UpdatedAt
			}
		}

		// Fall back to directory modification time.
		if nse.Modified == "" {
			if info, err := entry.Info(); err == nil {
				nse.Modified = info.ModTime().Format(time.RFC3339)
			}
		}

		sessions = append(sessions, nse)
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

// copilotSession manages a Copilot CLI conversation across multiple Send() calls.
// Each Send() spawns a new `copilot -p` process; --resume maintains session continuity.
type copilotSession struct {
	ctx       context.Context
	cfg       config.CopilotConfig
	security  *config.SecurityConfig
	sessionID string // Copilot native session ID for --resume

	output chan Output
	mu     sync.Mutex
	cmd    *exec.Cmd
	done   chan struct{}
	closed bool

	permHandler func(tool, description, resource string) bool
}

func (s *copilotSession) Send(ctx context.Context, input []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	sid := s.sessionID
	s.mu.Unlock()

	// -p takes the prompt as its value, other flags come separately.
	args := []string{"-p", string(input), "--silent", "--no-color"}

	// Permission mode from security config.
	permMode := ""
	if s.security != nil && s.security.PermissionMode != "" {
		permMode = s.security.PermissionMode
	}
	if permMode == "skip" {
		args = append(args, "--allow-all")
	}

	// Model.
	if s.cfg.Model != "" {
		args = append(args, "--model", s.cfg.Model)
	}

	// Autopilot mode.
	if s.cfg.MaxAutopilotContinues > 0 {
		args = append(args, "--autopilot", "--max-autopilot-continues", strconv.Itoa(s.cfg.MaxAutopilotContinues))
	}

	// Allowed tools — merge security config and profile config.
	allowedTools := s.cfg.AllowedTools
	if s.security != nil && len(s.security.AllowedTools) > 0 {
		allowedTools = s.security.AllowedTools
	}
	for _, tool := range allowedTools {
		args = append(args, "--allow-tool", tool)
	}

	// Denied tools.
	deniedTools := s.cfg.DeniedTools
	if s.security != nil && len(s.security.DeniedPaths) > 0 {
		deniedTools = append(deniedTools, s.security.DeniedPaths...)
	}
	for _, tool := range deniedTools {
		args = append(args, "--deny-tool", tool)
	}

	// Session continuity: use --resume with session ID, fallback to --continue.
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

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start copilot process: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.done = make(chan struct{})
	s.mu.Unlock()

	done := s.done

	// Read stdout and stderr in background.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			cp := make([]byte, len(line))
			copy(cp, line)
			s.output <- Output{Channel: "stdout", Data: cp}
		}
	}()

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

	// Wait for process to exit, then discover session ID and signal turn complete.
	go func() {
		wg.Wait()
		waitErr := cmd.Wait()
		_ = waitErr

		// After first send, try to discover the session ID from ~/.copilot/session-state/.
		if sid == "" {
			s.discoverSessionID()
		}

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

// discoverSessionID finds the most recently modified session in ~/.copilot/session-state/
// and stores its ID for future --resume calls.
func (s *copilotSession) discoverSessionID() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	sessionDir := filepath.Join(home, ".copilot", "session-state")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return
	}

	var newest string
	var newestTime time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = entry.Name()
		}
	}

	if newest != "" {
		s.mu.Lock()
		// Only set if we don't already have one (from SetResumeSessionID).
		if s.sessionID == "" {
			s.sessionID = newest
		}
		s.mu.Unlock()
	}
}

func (s *copilotSession) Output() <-chan Output {
	return s.output
}

func (s *copilotSession) Wait() error {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	return nil
}

func (s *copilotSession) Stop() error {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

func (s *copilotSession) Close() error {
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

func (s *copilotSession) ExitCode() *int {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		return &code
	}
	return nil
}

func (s *copilotSession) SetPermissionHandler(handler func(tool, description, resource string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permHandler = handler
}

// NativeHandle returns the Copilot native session ID.
func (s *copilotSession) NativeHandle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// SetResumeSessionID pre-seeds the native session ID so the first Send()
// uses --resume to continue an existing Copilot session.
func (s *copilotSession) SetResumeSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}

// LoadNativeHistory reads the Copilot session state and returns conversation
// history items. This pre-populates the UI when resuming an existing session.
func (s *copilotSession) LoadNativeHistory() []Output {
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

	sessionDir := filepath.Join(home, ".copilot", "session-state", sid)
	if _, err := os.Stat(sessionDir); err != nil {
		return nil
	}

	// Try to read conversation history from the session directory.
	// Copilot stores session data as JSON files; try common file names.
	var outputs []Output

	// Look for conversation/messages/history files.
	candidates := []string{"conversation.json", "messages.json", "history.json"}
	for _, name := range candidates {
		histPath := filepath.Join(sessionDir, name)
		data, err := os.ReadFile(histPath)
		if err != nil {
			continue
		}

		// Try parsing as array of message objects.
		var messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(data, &messages); err == nil {
			for _, msg := range messages {
				switch msg.Role {
				case "user":
					if msg.Content != "" {
						outputs = append(outputs, Output{Channel: "history_user", Data: []byte(msg.Content)})
					}
				case "assistant":
					if msg.Content != "" {
						outputs = append(outputs, Output{Channel: "history_assistant", Data: []byte(msg.Content)})
					}
				}
			}
			break
		}
	}

	if len(outputs) > 0 {
		outputs = append(outputs, Output{Channel: "system", Data: []byte("Session history loaded. Send a message to continue.")})
	}

	return outputs
}


