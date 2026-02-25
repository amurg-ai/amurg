package adapter

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// JobAdapter implements the generic-job profile.
// It executes a command per user message (run-to-completion).
type JobAdapter struct{}

func (a *JobAdapter) Start(ctx context.Context, cfg config.EndpointConfig) (AgentSession, error) {
	jobCfg := cfg.Job
	if jobCfg == nil {
		return nil, fmt.Errorf("generic-job endpoint %s: missing job config", cfg.ID)
	}

	return &jobSession{
		cfg:      *jobCfg,
		epID:     cfg.ID,
		security: cfg.Security,
		output:   make(chan Output, 64),
	}, nil
}

type jobSession struct {
	cfg      config.JobConfig
	epID     string
	security *config.SecurityConfig
	output   chan Output

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	waitErr  error
	exitCode *int
}

// ExitCode returns the exit code if the process has exited.
func (s *jobSession) ExitCode() *int {
	return s.exitCode
}

func (s *jobSession) Send(ctx context.Context, input []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build the command. Input is passed via stdin.
	args := make([]string, len(s.cfg.Args))
	copy(args, s.cfg.Args)

	timeout := 5 * time.Minute
	if s.cfg.MaxRuntime.Duration > 0 {
		timeout = s.cfg.MaxRuntime.Duration
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)

	cmd := exec.CommandContext(ctx, s.cfg.Command, args...)
	if s.cfg.WorkDir != "" {
		cmd.Dir = s.cfg.WorkDir
	} else if s.security != nil && s.security.Cwd != "" {
		cmd.Dir = s.security.Cwd
	}
	cmd.Env = os.Environ()
	for k, v := range s.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Pass input via stdin.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start job: %w", err)
	}

	s.cmd = cmd
	s.done = make(chan struct{})

	// Write input and close stdin.
	go func() {
		_, _ = stdin.Write(input)
		_ = stdin.Close()
	}()

	// Stream output.
	var wg sync.WaitGroup
	wg.Add(2)
	go s.readPipe(&wg, stdout, "stdout")
	go s.readPipe(&wg, stderr, "stderr")

	go func() {
		wg.Wait()
		s.waitErr = cmd.Wait()
		// Capture exit code.
		if cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			s.exitCode = &code
		}
		cancel()
		close(s.done)
	}()

	return nil
}

func (s *jobSession) Output() <-chan Output {
	return s.output
}

func (s *jobSession) Wait() error {
	if s.done != nil {
		<-s.done
	}
	return s.waitErr
}

func (s *jobSession) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		return s.cmd.Process.Kill()
	}
	return nil
}

func (s *jobSession) Close() error {
	_ = s.Stop()
	if s.done != nil {
		<-s.done
	}
	close(s.output)
	return nil
}

func (s *jobSession) readPipe(wg *sync.WaitGroup, r io.Reader, channel string) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, buf[:n])
			s.output <- Output{Channel: channel, Data: cp}
		}
		if err != nil {
			return
		}
	}
}
