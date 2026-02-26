package adapter

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// CLIAdapter implements the generic-cli profile.
// It spawns an interactive CLI process and pipes stdin/stdout/stderr.
type CLIAdapter struct{}

func (a *CLIAdapter) Start(ctx context.Context, cfg config.AgentConfig) (AgentSession, error) {
	cliCfg := cfg.CLI
	if cliCfg == nil {
		return nil, fmt.Errorf("generic-cli agent %s: missing cli config", cfg.ID)
	}

	cmd := exec.CommandContext(ctx, cliCfg.Command, cliCfg.Args...)
	if cliCfg.WorkDir != "" {
		cmd.Dir = cliCfg.WorkDir
	} else if cfg.Security != nil && cfg.Security.Cwd != "" {
		cmd.Dir = cfg.Security.Cwd
	}
	cmd.Env = os.Environ()
	for k, v := range cliCfg.Env {
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

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process: %w", err)
	}

	sess := &cliSession{
		cmd:    cmd,
		stdin:  stdin,
		output: make(chan Output, 64),
		done:   make(chan struct{}),
	}

	sess.wg.Add(2)
	go sess.readPipe(stdout, "stdout")
	go sess.readPipe(stderr, "stderr")

	// Close output channel when both readers are done.
	go func() {
		sess.wg.Wait()
		close(sess.output)
	}()

	// Wait for process exit in background.
	go func() {
		sess.waitErr = cmd.Wait()
		close(sess.done)
	}()

	return sess, nil
}

type cliSession struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	output  chan Output
	done    chan struct{}
	wg      sync.WaitGroup
	waitErr error
}

func (s *cliSession) Send(ctx context.Context, input []byte) error {
	// Append newline if not present (CLI convention).
	if len(input) > 0 && input[len(input)-1] != '\n' {
		input = append(input, '\n')
	}
	_, err := s.stdin.Write(input)
	return err
}

func (s *cliSession) Output() <-chan Output {
	return s.output
}

func (s *cliSession) Wait() error {
	<-s.done
	return s.waitErr
}

func (s *cliSession) Stop() error {
	if s.cmd.Process != nil {
		return s.cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

func (s *cliSession) Close() error {
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		// Ensure process is terminated.
		_ = s.cmd.Process.Kill()
	}
	<-s.done
	return nil
}

func (s *cliSession) readPipe(r io.Reader, channel string) {
	defer s.wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // up to 1MB lines
	for scanner.Scan() {
		line := scanner.Bytes()
		// Copy to avoid buffer reuse issues.
		cp := make([]byte, len(line))
		copy(cp, line)
		s.output <- Output{Channel: channel, Data: cp}
	}
}
