package session

import (
	"context"
	"fmt"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/adapter"
)

// SetTerminalOutputHandler must be called before creating sessions.
func (m *Manager) SetTerminalOutputHandler(fn func(protocol.TerminalOutput)) { m.onTerminalOutput = fn }

func (m *Manager) HandleTerminal(ctx context.Context, op string, req protocol.TerminalRequest) error {
	sess, ok := m.Get(req.SessionID)
	if !ok {
		if op != protocol.TypeTerminalAttach {
			return fmt.Errorf("terminal needs reattachment")
		}
		if err := m.Create(ctx, req.SessionID, req.AgentID, req.UserID, "standard"); err != nil {
			return err
		}
		sess, ok = m.Get(req.SessionID)
		if !ok {
			return fmt.Errorf("session closed during attachment")
		}
	}
	term, ok := sess.agent.(adapter.TerminalSession)
	if !ok {
		return fmt.Errorf("session does not use terminal passthrough")
	}
	switch op {
	case protocol.TypeTerminalAttach:
		return term.Attach(ctx, req.Cols, req.Rows)
	case protocol.TypeTerminalInput:
		return term.Input(ctx, req.Generation, req.Data)
	case protocol.TypeTerminalResize:
		return term.Resize(ctx, req.Generation, req.Cols, req.Rows)
	default:
		return fmt.Errorf("unknown terminal operation")
	}
}

func (m *Manager) IsTerminal(sessionID string) bool {
	sess, ok := m.Get(sessionID)
	if !ok {
		return false
	}
	_, ok = sess.agent.(adapter.TerminalSession)
	return ok
}
