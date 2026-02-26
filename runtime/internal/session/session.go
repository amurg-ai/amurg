// Package session manages the mapping between hub sessions and native agent sessions.
package session

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amurg-ai/amurg/runtime/internal/adapter"
)

// State represents a session's lifecycle state.
type State string

const (
	StateActive    State = "active"
	StateResponding State = "responding"
	StateIdle      State = "idle"
	StateClosed   State = "closed"
)

// OutputHandler is called for each piece of agent output.
type OutputHandler func(sessionID string, output adapter.Output, final bool)

// Session represents a single active session between a hub user and an agent.
type Session struct {
	ID        string
	AgentID   string
	UserID    string
	CreatedAt time.Time

	agent   adapter.AgentSession
	state   atomic.Value // State
	logger  *slog.Logger
	onOutput OutputHandler

	mu       sync.Mutex
	seq      int64
}

// NewSession creates a new session wrapping an agent session.
func NewSession(id, agentID, userID string, agent adapter.AgentSession, onOutput OutputHandler, logger *slog.Logger) *Session {
	s := &Session{
		ID:        id,
		AgentID:   agentID,
		UserID:    userID,
		CreatedAt: time.Now(),
		agent:     agent,
		onOutput:  onOutput,
		logger:    logger.With("session_id", id, "agent_id", agentID),
	}
	s.state.Store(StateActive)
	return s
}

// State returns the current session state.
func (s *Session) State() State {
	return s.state.Load().(State)
}

// Send delivers a user message to the agent and starts reading output.
func (s *Session) Send(ctx context.Context, input []byte, idleTimeout time.Duration) error {
	s.state.Store(StateResponding)
	s.logger.Info("sending user input", "bytes", len(input))

	if err := s.agent.Send(ctx, input); err != nil {
		s.state.Store(StateActive)
		return err
	}

	// Start draining output in background.
	go s.drainOutput(idleTimeout)

	return nil
}

// Stop requests the agent to stop.
func (s *Session) Stop() error {
	s.logger.Info("stopping session")
	return s.agent.Stop()
}

// Close terminates the session and releases resources.
func (s *Session) Close() error {
	s.state.Store(StateClosed)
	s.logger.Info("closing session")
	return s.agent.Close()
}

// drainOutput reads from the agent output channel and forwards to the handler.
// It uses idle timeout to detect turn completion for interactive profiles.
func (s *Session) drainOutput(idleTimeout time.Duration) {
	outCh := s.agent.Output()
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()

	for {
		select {
		case out, ok := <-outCh:
			if !ok {
				// Channel closed = agent done. Check for exit code.
				s.mu.Lock()
				s.seq++
				s.mu.Unlock()
				s.state.Store(StateActive)
				finalOut := adapter.Output{Channel: "system", Data: nil}
				if ec, ok := s.agent.(adapter.ExitCoder); ok {
					finalOut.ExitCode = ec.ExitCode()
				}
				s.onOutput(s.ID, finalOut, true)
				return
			}

			s.mu.Lock()
			s.seq++
			s.mu.Unlock()

			// ExitCode on an output signals deterministic turn completion
			// (e.g. claude-code process exited). Emit as final and return
			// immediately instead of waiting for idle timeout.
			if out.ExitCode != nil {
				s.state.Store(StateActive)
				s.onOutput(s.ID, out, true)
				return
			}

			s.onOutput(s.ID, out, false)

			// Reset idle timer.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idleTimeout)

		case <-timer.C:
			// Idle timeout = infer turn completion.
			s.logger.Debug("idle timeout, inferring turn complete")
			s.state.Store(StateActive)
			s.onOutput(s.ID, adapter.Output{Channel: "system", Data: nil}, true)
			return
		}
	}
}
