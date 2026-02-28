package dashboard

import (
	"encoding/json"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amurg-ai/amurg/runtime/internal/eventbus"
	"github.com/amurg-ai/amurg/runtime/internal/ipc"
)

// Attach connects to a running daemon via IPC and displays the dashboard TUI.
// Returns true when the user quits the dashboard (daemon keeps running).
func Attach(socketPath string) (ok bool, err error) {
	client, err := ipc.Dial(socketPath)
	if err != nil {
		return false, fmt.Errorf("connect to runtime: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Get initial status and sessions.
	statusResp, err := client.Call("status", nil)
	if err != nil {
		return false, fmt.Errorf("query status: %w", err)
	}
	var status ipc.StatusResult
	if err := json.Unmarshal(statusResp.Data, &status); err != nil {
		return false, fmt.Errorf("decode status: %w", err)
	}

	sessResp, err := client.Call("sessions", nil)
	if err != nil {
		return false, fmt.Errorf("query sessions: %w", err)
	}
	var sessResult ipc.SessionsResult
	if err := json.Unmarshal(sessResp.Data, &sessResult); err != nil {
		return false, fmt.Errorf("decode sessions: %w", err)
	}

	// Subscribe to all events.
	if err := client.Subscribe(); err != nil {
		return false, fmt.Errorf("subscribe: %w", err)
	}

	m := NewModel(status, sessResult.Sessions)

	p := tea.NewProgram(m, tea.WithAltScreen())

	// refreshState fetches current status and sessions and sends updates to the TUI.
	refreshState := func() {
		resp, err := client.Call("status", nil)
		if err != nil {
			return
		}
		var s ipc.StatusResult
		if json.Unmarshal(resp.Data, &s) == nil {
			p.Send(StatusUpdateMsg{Status: s})
		}

		sr, err := client.Call("sessions", nil)
		if err != nil {
			return
		}
		var ss ipc.SessionsResult
		if json.Unmarshal(sr.Data, &ss) == nil {
			p.Send(SessionsUpdateMsg{Sessions: ss.Sessions})
		}
	}

	// Forward IPC events to the TUI, and trigger immediate refresh on session events.
	go func() {
		for evt := range client.Events() {
			p.Send(EventMsg{Type: evt.Type, Data: evt.Data})
			// Session lifecycle events should refresh the panels immediately.
			switch evt.Type {
			case "session.created", "session.closed", "session.state":
				refreshState()
			}
		}
	}()

	// Periodically refresh status and sessions.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			refreshState()
		}
	}()

	if _, err := p.Run(); err != nil {
		return false, fmt.Errorf("TUI error: %w", err)
	}

	return true, nil
}

// NewInlineModel creates a dashboard model that subscribes directly to the
// event bus (same-process mode for `amurg-runtime run`).
func NewInlineModel(bus *eventbus.Bus, status ipc.StatusResult, sessions []ipc.SessionInfo) (Model, func(p *tea.Program)) {
	m := NewModel(status, sessions)

	// Return a function that starts forwarding events.
	startForwarding := func(p *tea.Program) {
		ch := bus.Subscribe()
		go func() {
			for evt := range ch {
				p.Send(EventMsg{Type: evt.Type, Data: evt.Data})
			}
		}()

		// Periodic status refresh via bus-based state.
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				// For inline mode, the runtime publishes status events.
				// The dashboard auto-updates from EventMsg.
				// We don't need to poll since events stream in real-time.
			}
		}()
	}

	return m, startForwarding
}
