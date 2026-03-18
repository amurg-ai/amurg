package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

const codexTMuxSubmitDelay = 200 * time.Millisecond

// codexTMuxSession keeps a native Codex TUI alive inside tmux so Amurg can
// relay interactive prompts and approvals over the hub.
type codexTMuxSession struct {
	ctx      context.Context
	cfg      config.CodexConfig
	security *config.SecurityConfig
	threadID string
	workDir  string

	output chan Output

	mu             sync.Mutex
	closed         bool
	started        bool
	restartPending bool
	discovering    bool
	sessionName    string
	paneTarget     string
	logDir         string
	logPath        string
	startedAt      time.Time
	monitorStop    chan struct{}
	monitorDone    chan struct{}
	closeDone      chan struct{}
	closeOnce      sync.Once
}

func newCodexTMuxSession(ctx context.Context, cfg config.CodexConfig, security *config.SecurityConfig) *codexTMuxSession {
	return &codexTMuxSession{
		ctx:       ctx,
		cfg:       cfg,
		security:  security,
		output:    make(chan Output, 256),
		closeDone: make(chan struct{}),
	}
}

func buildCodexTMuxArgs(cfg config.CodexConfig, security *config.SecurityConfig, threadID, workDir string) []string {
	args := make([]string, 0, 16)
	args = append(args, "--no-alt-screen")

	permMode := cfg.ApprovalMode
	if security != nil && security.PermissionMode != "" {
		switch security.PermissionMode {
		case "skip", "bypassPermissions", "dontAsk":
			permMode = "never"
		case "strict":
			permMode = "untrusted"
		case "auto", "acceptEdits", "plan":
			permMode = "on-request"
		}
	}
	if permMode == "skip" {
		permMode = "never"
	}
	if permMode != "" {
		args = append(args, "-a", permMode)
	}

	sandboxMode := cfg.SandboxMode
	if sandboxMode != "" {
		args = append(args, "--sandbox", sandboxMode)
	}
	if cfg.FullAuto && sandboxMode == "" && permMode == "" {
		args = append(args, "--full-auto")
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.Profile != "" {
		args = append(args, "--profile", cfg.Profile)
	}
	if workDir != "" {
		args = append(args, "-C", workDir)
	}
	for _, dir := range codexMergeUnique(cfg.AdditionalDirs, securityAllowedPaths(security)) {
		args = append(args, "--add-dir", dir)
	}
	if threadID != "" {
		args = append(args, "resume", threadID)
	}
	return args
}

func (s *codexTMuxSession) ensureStarted() error {
	if err := ensureTMuxInstalled(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	if s.sessionName == "" {
		s.sessionName = tmuxSessionName("amurg-codex")
		s.paneTarget = s.sessionName + ":0.0"
	}
	needsRestart := s.restartPending
	started := s.started && tmuxHasSession(s.sessionName)
	s.mu.Unlock()

	if needsRestart {
		_ = s.resetTMuxSession()
		started = false
	}
	if started {
		return nil
	}

	workDir := resolveWorkDir(s.cfg.WorkDir, s.security)
	logDir, err := os.MkdirTemp("", "amurg-codex-tmux-*")
	if err != nil {
		return fmt.Errorf("create tmux log dir: %w", err)
	}
	logPath := filepath.Join(logDir, "pane.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		_ = os.RemoveAll(logDir)
		return fmt.Errorf("create tmux log file: %w", err)
	}

	s.mu.Lock()
	threadID := s.threadID
	s.mu.Unlock()

	command := append([]string{s.cfg.Command}, buildCodexTMuxArgs(s.cfg, s.security, threadID, workDir)...)
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
	s.restartPending = false
	s.startedAt = startedAt
	s.monitorStop = monitorStop
	s.monitorDone = monitorDone
	s.mu.Unlock()

	go s.monitorPane(logPath, startedAt, monitorStop, monitorDone)
	s.handleStartupPrompts()
	return nil
}

func (s *codexTMuxSession) monitorPane(logPath string, startedAt time.Time, stop <-chan struct{}, done chan<- struct{}) {
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
			s.maybeDiscoverThreadID(startedAt)
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
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, offset, err
	}
	return data, info.Size(), nil
}

func (s *codexTMuxSession) handleStartupPrompts() {
	for range 12 {
		s.mu.Lock()
		target := s.paneTarget
		closed := s.closed
		s.mu.Unlock()
		if closed || target == "" {
			return
		}

		screen, err := tmuxCapturePane(target, 80)
		if err != nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		switch {
		case strings.Contains(screen, "Update available!"):
			_ = tmuxSendKeys(target, "Down", "Enter")
			time.Sleep(500 * time.Millisecond)
		case strings.Contains(screen, "Do you trust the contents of this directory?"):
			_ = tmuxSendKeys(target, "Enter")
			time.Sleep(500 * time.Millisecond)
		default:
			if strings.Contains(screen, "OpenAI Codex") || strings.Contains(screen, "gpt-") {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
}

func (s *codexTMuxSession) maybeDiscoverThreadID(startedAt time.Time) {
	s.mu.Lock()
	if s.threadID != "" || s.discovering || s.workDir == "" {
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

		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			tid := findLatestCodexThreadIDForProject(workDir, startedAt)
			if tid != "" {
				s.mu.Lock()
				if s.threadID == "" {
					s.threadID = tid
				}
				s.mu.Unlock()
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
}

func findLatestCodexThreadIDForProject(workDir string, since time.Time) string {
	sessionsDir := codexSessionsDir()
	if sessionsDir == "" {
		return ""
	}
	workDir = filepath.Clean(workDir)
	since = since.Add(-2 * time.Second)

	var (
		latestID  string
		latestMod time.Time
	)
	_ = filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		if info.ModTime().Before(since) {
			return nil
		}
		entry := parseCodexRolloutHeader(path, info)
		if entry.SessionID == "" || entry.ProjectPath == "" {
			return nil
		}
		if filepath.Clean(entry.ProjectPath) != workDir {
			return nil
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestID = entry.SessionID
		}
		return nil
	})
	return latestID
}

func (s *codexTMuxSession) resetTMuxSession() error {
	s.mu.Lock()
	if s.monitorStop != nil {
		close(s.monitorStop)
		s.monitorStop = nil
	}
	monitorDone := s.monitorDone
	s.monitorDone = nil
	s.started = false
	s.restartPending = false
	sessionName := s.sessionName
	logDir := s.logDir
	s.logDir = ""
	s.logPath = ""
	s.mu.Unlock()

	if monitorDone != nil {
		<-monitorDone
	}
	if err := tmuxKillSession(sessionName); err != nil {
		return err
	}
	if logDir != "" {
		_ = os.RemoveAll(logDir)
	}
	return nil
}

func (s *codexTMuxSession) Send(ctx context.Context, input []byte) error {
	_ = ctx
	if err := s.ensureStarted(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	target := s.paneTarget
	startedAt := s.startedAt
	s.mu.Unlock()

	if err := tmuxSendLiteral(target, string(input)); err != nil {
		return err
	}

	// Codex suppresses Enter immediately after a fast paste burst, so wait long
	// enough for the composer to flush before submitting the prompt.
	time.Sleep(codexTMuxSubmitDelay)

	if err := tmuxSendKeys(target, "Enter"); err != nil {
		return err
	}
	s.maybeDiscoverThreadID(startedAt)
	return nil
}

func (s *codexTMuxSession) Output() <-chan Output {
	return s.output
}

func (s *codexTMuxSession) Wait() error {
	<-s.closeDone
	return nil
}

func (s *codexTMuxSession) Stop() error {
	s.mu.Lock()
	target := s.paneTarget
	started := s.started
	s.mu.Unlock()
	if !started || target == "" {
		return nil
	}
	return tmuxSendKeys(target, "C-c")
}

func (s *codexTMuxSession) UpdateSecurity(security *config.SecurityConfig) (restartRequired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.security = security
	s.restartPending = true
	return false
}

func (s *codexTMuxSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.monitorStop != nil {
			close(s.monitorStop)
			s.monitorStop = nil
		}
		monitorDone := s.monitorDone
		s.monitorDone = nil
		sessionName := s.sessionName
		logDir := s.logDir
		s.logDir = ""
		s.logPath = ""
		s.mu.Unlock()

		if monitorDone != nil {
			<-monitorDone
		}
		if killErr := tmuxKillSession(sessionName); killErr != nil {
			err = killErr
		}
		if logDir != "" {
			_ = os.RemoveAll(logDir)
		}
		close(s.output)
		close(s.closeDone)
	})
	return err
}

func (s *codexTMuxSession) SetPermissionHandler(handler func(tool, description, resource string) bool) {
	_ = handler
}

func (s *codexTMuxSession) NativeHandle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

func (s *codexTMuxSession) SetResumeSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threadID = id
	s.restartPending = true
}

func (s *codexTMuxSession) LoadNativeHistory() []Output {
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
		case "command_execution":
			if item.Command != "" {
				outputs = append(outputs, Output{Channel: "history_tool", Data: []byte(item.Command)})
			}
		case "file_change":
			for _, ch := range item.Changes {
				outputs = append(outputs, Output{Channel: "history_tool", Data: []byte(ch.Kind + ": " + ch.Path)})
			}
		}
	}

	return outputs
}
