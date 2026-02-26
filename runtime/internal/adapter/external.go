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
	"sync"

	"github.com/google/uuid"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// ExternalAdapter implements the external profile.
// It spawns a long-lived adapter process that communicates via JSON-Lines
// over stdin/stdout. The adapter process handles multiple sessions
// multiplexed by session_id.
type ExternalAdapter struct{}

// externalMsg is the JSON-Lines message format between runtime and adapter process.
type externalMsg struct {
	Type         string `json:"type"`
	SessionID    string `json:"session_id"`
	UserID       string `json:"user_id,omitempty"`
	Content      string `json:"content,omitempty"`
	Channel      string `json:"channel,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Description  string `json:"description,omitempty"`
	Resource     string `json:"resource,omitempty"`
	Approved     *bool  `json:"approved,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	FileMimeType string `json:"file_mime_type,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
}

func (a *ExternalAdapter) Start(ctx context.Context, cfg config.AgentConfig) (AgentSession, error) {
	extCfg := cfg.External
	if extCfg == nil {
		return nil, fmt.Errorf("external agent %s: missing external config", cfg.ID)
	}

	cmd := exec.CommandContext(ctx, extCfg.Command, extCfg.Args...)
	if extCfg.WorkDir != "" {
		cmd.Dir = extCfg.WorkDir
	} else if cfg.Security != nil && cfg.Security.Cwd != "" {
		cmd.Dir = cfg.Security.Cwd
	}
	cmd.Env = os.Environ()
	for k, v := range extCfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Redirect stderr to os.Stderr for adapter debugging.
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start external adapter: %w", err)
	}

	sess := &externalSession{
		cmd:       cmd,
		stdin:     stdin,
		output:    make(chan Output, 64),
		done:      make(chan struct{}),
		sessionID: "", // set by first Send
		security:  cfg.Security,
	}

	// Read JSON-Lines from stdout in background.
	go sess.readLoop(stdout)

	// Wait for process exit in background.
	go func() {
		sess.waitErr = cmd.Wait()
		close(sess.done)
	}()

	return sess, nil
}

type externalSession struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	output      chan Output
	done        chan struct{}
	sessionID   string
	mu          sync.Mutex
	waitErr     error
	closed      bool
	security    *config.SecurityConfig
	permHandler func(tool, description, resource string) bool
}

func (s *externalSession) DeliverFile(filePath, fileName, mimeType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := externalMsg{
		Type:         "file.input",
		SessionID:    s.sessionID,
		FileName:     fileName,
		FileMimeType: mimeType,
		FilePath:     filePath,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal file input: %w", err)
	}
	data = append(data, '\n')
	_, err = s.stdin.Write(data)
	return err
}

func (s *externalSession) SetPermissionHandler(handler func(tool, description, resource string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permHandler = handler
}

func (s *externalSession) Send(ctx context.Context, input []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msgType := "user.input"
	if s.sessionID == "" {
		// First send — generate a unique session ID for multiplexing.
		s.sessionID = uuid.New().String()
		startMsg := externalMsg{Type: "session.start", SessionID: s.sessionID}
		if data, err := json.Marshal(startMsg); err == nil {
			data = append(data, '\n')
			_, _ = s.stdin.Write(data)
		}
		msgType = "user.input"
	}

	msg := externalMsg{
		Type:      msgType,
		SessionID: s.sessionID,
		Content:   string(input),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal input: %w", err)
	}
	data = append(data, '\n')
	_, err = s.stdin.Write(data)
	return err
}

func (s *externalSession) Output() <-chan Output {
	return s.output
}

func (s *externalSession) Wait() error {
	<-s.done
	return s.waitErr
}

func (s *externalSession) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := externalMsg{Type: "stop", SessionID: s.sessionID}
	if data, err := json.Marshal(msg); err == nil {
		data = append(data, '\n')
		_, _ = s.stdin.Write(data)
	}
	return nil
}

func (s *externalSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// Send session.close.
	msg := externalMsg{Type: "session.close", SessionID: s.sessionID}
	if data, err := json.Marshal(msg); err == nil {
		data = append(data, '\n')
		_, _ = s.stdin.Write(data)
	}
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	<-s.done
	return nil
}

func (s *externalSession) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg externalMsg
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "output":
			ch := msg.Channel
			if ch == "" {
				ch = "stdout"
			}
			s.output <- Output{Channel: ch, Data: []byte(msg.Content)}
		case "turn.complete":
			// Signal turn completion by closing and reopening the output channel.
			// The session manager drains output, detects channel close, and sends turn.completed.
		case "file.output":
			// Adapter produced a file. Read it from disk and emit as file Output.
			filePath := msg.FilePath
			if filePath == "" {
				continue
			}
			fileData, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}
			fileName := msg.FileName
			if fileName == "" {
				fileName = filepath.Base(filePath)
			}
			mimeType := msg.FileMimeType
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			s.output <- Output{
				Channel:      "file",
				Data:         fileData,
				FileName:     fileName,
				FileMimeType: mimeType,
			}
		case "permission.request":
			s.mu.Lock()
			handler := s.permHandler
			s.mu.Unlock()
			if handler != nil {
				tool := msg.Tool
				desc := msg.Description
				resource := msg.Resource
				reqID := msg.RequestID
				go func() {
					approved := handler(tool, desc, resource)
					resp := externalMsg{
						Type:      "permission.response",
						SessionID: msg.SessionID,
						RequestID: reqID,
						Approved:  &approved,
					}
					if data, err := json.Marshal(resp); err == nil {
						data = append(data, '\n')
						s.mu.Lock()
						_, _ = s.stdin.Write(data)
						s.mu.Unlock()
					}
				}()
			}
		}
	}
	close(s.output)
}
