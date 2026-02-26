package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// HTTPAdapter implements the generic-http profile.
// It sends user messages as HTTP requests and streams/buffers the response.
type HTTPAdapter struct{}

func (a *HTTPAdapter) Start(ctx context.Context, cfg config.AgentConfig) (AgentSession, error) {
	httpCfg := cfg.HTTP
	if httpCfg == nil {
		return nil, fmt.Errorf("generic-http agent %s: missing http config", cfg.ID)
	}

	method := httpCfg.Method
	if method == "" {
		method = http.MethodPost
	}

	timeout := 60 * time.Second
	if httpCfg.Timeout.Duration > 0 {
		timeout = httpCfg.Timeout.Duration
	}

	return &httpSession{
		baseURL: httpCfg.BaseURL,
		method:  method,
		headers: httpCfg.Headers,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
		output:  make(chan Output, 64),
	}, nil
}

type httpSession struct {
	baseURL string
	method  string
	headers map[string]string
	timeout time.Duration
	client  *http.Client
	output  chan Output
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
}

func (s *httpSession) Send(ctx context.Context, input []byte) error {
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})

	req, err := http.NewRequestWithContext(ctx, s.method, s.baseURL, bytes.NewReader(input))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	go func() {
		defer close(s.done)

		resp, err := s.client.Do(req)
		if err != nil {
			s.err = err
			s.output <- Output{Channel: "system", Data: []byte(fmt.Sprintf("HTTP error: %v", err))}
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			s.output <- Output{
				Channel: "stderr",
				Data:    []byte(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, body)),
			}
			return
		}

		// Stream response body in chunks.
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				s.output <- Output{Channel: "stdout", Data: cp}
			}
			if err != nil {
				if err != io.EOF {
					s.err = err
				}
				return
			}
		}
	}()

	return nil
}

func (s *httpSession) Output() <-chan Output {
	return s.output
}

func (s *httpSession) Wait() error {
	if s.done != nil {
		<-s.done
	}
	return s.err
}

func (s *httpSession) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *httpSession) Close() error {
	_ = s.Stop()
	if s.done != nil {
		<-s.done
	}
	close(s.output)
	return nil
}
