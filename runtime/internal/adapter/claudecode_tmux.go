package adapter

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// claudeTMuxSession keeps a native Claude Code TUI alive inside tmux so
// Amurg can relay fully interactive input without forcing print mode.
type claudeTMuxSession struct {
	ctx            context.Context
	cfg            config.ClaudeCodeConfig
	security       *config.SecurityConfig
	sessionID      string
	resumeExplicit bool

	output chan Output

	mu          sync.Mutex
	closed      bool
	started     bool
	discovering bool
	sessionName string
	paneTarget  string
	workDir     string
	logDir      string
	logPath     string
	startedAt   time.Time
	monitorStop chan struct{}
	monitorDone chan struct{}
	closeDone   chan struct{}
	closeOnce   sync.Once
}

func newClaudeTMuxSession(ctx context.Context, cfg config.ClaudeCodeConfig, security *config.SecurityConfig) *claudeTMuxSession {
	return &claudeTMuxSession{
		ctx:       ctx,
		cfg:       cfg,
		security:  security,
		output:    make(chan Output, 256),
		closeDone: make(chan struct{}),
	}
}

func (s *claudeTMuxSession) ensureStarted() error {
	if err := ensureTMuxInstalled(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	if s.sessionName == "" {
		s.sessionName = tmuxSessionName("amurg-claude")
		s.paneTarget = s.sessionName + ":0.0"
	}
	started := s.started && tmuxHasSession(s.sessionName)
	s.mu.Unlock()

	if started {
		return nil
	}

	workDir := resolveWorkDir(s.cfg.WorkDir, s.security)
	logDir, err := os.MkdirTemp("", "amurg-claude-tmux-*")
	if err != nil {
		return fmt.Errorf("create tmux log dir: %w", err)
	}
	logPath := filepath.Join(logDir, "pane.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		_ = os.RemoveAll(logDir)
		return fmt.Errorf("create tmux log file: %w", err)
	}

	s.mu.Lock()
	resumeID := ""
	if s.resumeExplicit {
		resumeID = s.sessionID
	}
	s.mu.Unlock()

	command := buildClaudeTMuxCommand(s.cfg, s.security, resumeID)
	if err := tmuxCreateSession(s.sessionName, workDir, command); err != nil {
		_ = os.RemoveAll(logDir)
		return err
	}
	if err := tmuxPipePane(s.paneTarget, logPath); err != nil {
		_ = tmuxKillSession(s.sessionName)
		_ = os.RemoveAll(logDir)
		return err
	}

	monitorStop := make(chan struct{})
	monitorDone := make(chan struct{})
	startedAt := time.Now()

	s.mu.Lock()
	if s.logDir != "" {
		_ = os.RemoveAll(s.logDir)
	}
	s.workDir = workDir
	s.logDir = logDir
	s.logPath = logPath
	s.started = true
	s.startedAt = startedAt
	s.monitorStop = monitorStop
	s.monitorDone = monitorDone
	s.mu.Unlock()

	go s.monitorPane(logPath, startedAt, monitorStop, monitorDone)
	return nil
}

func buildClaudeTMuxCommand(cfg config.ClaudeCodeConfig, security *config.SecurityConfig, resumeID string) []string {
	args, skipPerms := buildClaudeTMuxArgs(cfg, security, resumeID)
	command := []string{"env", "-u", "CLAUDECODE", "-u", "CLAUDE_CODE_ENTRYPOINT"}
	if skipPerms {
		command = append(command, "CLAUDE_DANGEROUS_SKIP_PERMISSIONS=true")
	}

	keys := make([]string, 0, len(cfg.Env))
	for key := range cfg.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		command = append(command, key+"="+cfg.Env[key])
	}

	binary := cfg.Command
	if binary == "" {
		binary = "claude"
	}
	command = append(command, binary)
	command = append(command, args...)
	return command
}

func buildClaudeTMuxArgs(cfg config.ClaudeCodeConfig, security *config.SecurityConfig, resumeID string) ([]string, bool) {
	args := make([]string, 0, 16)
	skipPerms := false

	permMode := cfg.PermissionMode
	if security != nil && security.PermissionMode != "" {
		permMode = security.PermissionMode
	}
	switch permMode {
	case "dangerously-skip-permissions", "skip", "bypassPermissions":
		args = append(args, "--dangerously-skip-permissions")
		skipPerms = true
	case "acceptEdits":
		args = append(args, "--permission-mode", "acceptEdits")
	case "plan":
		args = append(args, "--permission-mode", "plan")
	case "default", "auto", "", "strict":
		// Claude's native interactive mode uses its default behavior here.
	}

	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(cfg.MaxTurns))
	}

	allowedTools := cfg.AllowedTools
	if security != nil && len(security.AllowedTools) > 0 {
		allowedTools = security.AllowedTools
	}
	for _, tool := range allowedTools {
		args = append(args, "--allowedTools", tool)
	}

	disallowedTools := cfg.DisallowedTools
	if security != nil && len(security.DisallowedTools) > 0 {
		disallowedTools = security.DisallowedTools
	}
	for _, tool := range disallowedTools {
		args = append(args, "--disallowedTools", tool)
	}

	if cfg.SystemPrompt != "" {
		args = append(args, "--system-prompt", cfg.SystemPrompt)
	}
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}

	return args, skipPerms
}

func (s *claudeTMuxSession) Send(_ context.Context, input []byte) error {
	if err := s.ensureStarted(); err != nil {
		return err
	}

	s.mu.Lock()
	target := s.paneTarget
	startedAt := s.startedAt
	s.mu.Unlock()

	if err := tmuxSendLiteral(target, string(input)); err != nil {
		return err
	}
	if err := tmuxSendKeys(target, "Enter"); err != nil {
		return err
	}

	s.maybeDiscoverSessionID(startedAt)
	return nil
}

func (s *claudeTMuxSession) Output() <-chan Output {
	return s.output
}

func (s *claudeTMuxSession) Wait() error {
	<-s.closeDone
	return nil
}

func (s *claudeTMuxSession) Stop() error {
	s.mu.Lock()
	target := s.paneTarget
	s.mu.Unlock()
	if target == "" {
		return nil
	}
	return tmuxSendKeys(target, "C-c")
}

func (s *claudeTMuxSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		sessionName := s.sessionName
		logDir := s.logDir
		monitorStop := s.monitorStop
		monitorDone := s.monitorDone
		s.mu.Unlock()

		if monitorStop != nil {
			close(monitorStop)
		}
		if sessionName != "" {
			_ = tmuxKillSession(sessionName)
		}
		if monitorDone != nil {
			<-monitorDone
		}
		if logDir != "" {
			_ = os.RemoveAll(logDir)
		}

		close(s.output)
		close(s.closeDone)
	})
	return nil
}

func (s *claudeTMuxSession) ExitCode() *int {
	return nil
}

func (s *claudeTMuxSession) UpdateSecurity(security *config.SecurityConfig) (restartRequired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.security = security
	return true
}

func (s *claudeTMuxSession) NativeHandle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *claudeTMuxSession) SetResumeSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
	s.resumeExplicit = true
}

func (s *claudeTMuxSession) LoadNativeHistory() []Output {
	s.mu.Lock()
	sid := s.sessionID
	s.mu.Unlock()
	return loadClaudeNativeHistory(sid)
}

func (s *claudeTMuxSession) monitorPane(logPath string, startedAt time.Time, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var offset int64
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}

		data, newOffset, err := readTMuxLog(logPath, offset)
		if err == nil && len(data) > 0 {
			offset = newOffset
			chunk := make([]byte, len(data))
			copy(chunk, data)
			s.output <- Output{Channel: "stdout", Data: chunk}
			s.maybeDiscoverSessionID(startedAt)
		}

		s.mu.Lock()
		sessionName := s.sessionName
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		if !tmuxHasSession(sessionName) {
			s.mu.Lock()
			if s.sessionName == sessionName {
				s.started = false
			}
			s.mu.Unlock()
			return
		}
	}
}

func (s *claudeTMuxSession) maybeDiscoverSessionID(startedAt time.Time) {
	s.mu.Lock()
	if s.sessionID != "" || s.discovering {
		s.mu.Unlock()
		return
	}
	workDir := s.workDir
	s.discovering = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.discovering = false
			s.mu.Unlock()
		}()

		sid := findLatestClaudeSessionID(workDir, startedAt)
		if sid == "" {
			return
		}

		s.mu.Lock()
		if s.sessionID == "" {
			s.sessionID = sid
		}
		s.mu.Unlock()
	}()
}

func readTMuxLog(path string, offset int64) ([]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, offset, err
	}
	if info.Size() <= offset {
		return nil, offset, nil
	}

	if _, err := file.Seek(offset, 0); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, offset, err
	}
	return data, offset + int64(len(data)), nil
}

func findLatestClaudeSessionID(workDir string, since time.Time) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	projectsDir := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}

	preferredDir := ""
	if workDir != "" {
		preferredDir = filepath.Join(projectsDir, encodeProjectPath(workDir))
	}

	bestID := ""
	bestTime := time.Time{}
	scan := func(dir string) {
		files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for _, file := range files {
			info, err := os.Stat(file)
			if err != nil {
				continue
			}
			modified := info.ModTime()
			if modified.Before(since.Add(-10 * time.Second)) {
				continue
			}
			if modified.After(bestTime) {
				bestTime = modified
				bestID = strings.TrimSuffix(filepath.Base(file), ".jsonl")
			}
		}
	}

	if preferredDir != "" {
		scan(preferredDir)
		if bestID != "" {
			return bestID
		}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		scan(filepath.Join(projectsDir, entry.Name()))
	}
	return bestID
}
