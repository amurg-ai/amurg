package dashboard

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amurg-ai/amurg/runtime/internal/ipc"
	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

type sessionsModel struct {
	items  []ipc.SessionInfo
	cursor int
}

func newSessions(sessions []ipc.SessionInfo) sessionsModel {
	return sessionsModel{items: sessions}
}

func (s *sessionsModel) update(sessions []ipc.SessionInfo) {
	s.items = sessions
	if s.cursor >= len(s.items) {
		s.cursor = max(0, len(s.items)-1)
	}
}

func (s sessionsModel) Update(msg tea.Msg) (sessionsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if s.cursor < len(s.items)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "G":
			s.cursor = max(0, len(s.items)-1)
		case "g":
			s.cursor = 0
		}
	}
	return s, nil
}

func (s sessionsModel) View() string {
	if len(s.items) == 0 {
		return tui.Dimmed.Render("  No active sessions")
	}

	// Table header.
	headerStyle := lipgloss.NewStyle().Foreground(tui.ColorSubtle).Bold(true)
	header := fmt.Sprintf("  %-10s %-20s %-12s %-12s %s",
		headerStyle.Render("ID"),
		headerStyle.Render("AGENT"),
		headerStyle.Render("USER"),
		headerStyle.Render("STATE"),
		headerStyle.Render("AGE"),
	)

	rows := header + "\n"
	for i, sess := range s.items {
		cursor := "  "
		style := lipgloss.NewStyle()
		if i == s.cursor {
			cursor = tui.Selected.Render("> ")
			style = style.Bold(true)
		}

		stateStyle := stateColor(sess.State)
		age := formatAge(sess.CreatedAt)

		shortID := sess.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		agentName := sess.AgentName
		if agentName == "" {
			agentName = sess.AgentID
		}
		if len(agentName) > 18 {
			agentName = agentName[:18]
		}

		userID := sess.UserID
		if len(userID) > 10 {
			userID = userID[:10]
		}

		row := fmt.Sprintf("%-10s %-20s %-12s %-12s %s",
			style.Render(shortID),
			style.Render(agentName),
			style.Render(userID),
			stateStyle.Render(sess.State),
			style.Render(age),
		)
		rows += cursor + row + "\n"
	}

	return rows
}

func (s sessionsModel) height() int {
	return min(len(s.items)+2, 12) // header + rows, max 12
}

func stateColor(state string) lipgloss.Style {
	switch state {
	case "responding":
		return lipgloss.NewStyle().Foreground(tui.ColorSuccess)
	case "active":
		return lipgloss.NewStyle().Foreground(tui.ColorAccent)
	case "idle":
		return lipgloss.NewStyle().Foreground(tui.ColorMuted)
	default:
		return lipgloss.NewStyle().Foreground(tui.ColorText)
	}
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
