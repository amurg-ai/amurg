package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/amurg-ai/amurg/runtime/internal/ipc"
	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

type headerModel struct {
	status ipc.StatusResult
}

func newHeader(status ipc.StatusResult) headerModel {
	return headerModel{status: status}
}

func (h *headerModel) update(status ipc.StatusResult) {
	h.status = status
}

func (h headerModel) View(width int) string {
	left := tui.Title.Render("Amurg Runtime")

	hubURL := h.status.HubURL
	dot := tui.StatusDot(h.status.HubConnected, h.status.Reconnecting)
	statusLabel := tui.StatusText(h.status.HubConnected, h.status.Reconnecting)

	right := fmt.Sprintf("%s  %s %s", hubURL, dot, statusLabel)

	uptime := h.formatUptime()
	info := fmt.Sprintf("  Runtime: %s   Sessions: %d/%d   Uptime: %s",
		h.status.RuntimeID, h.status.Sessions, h.status.MaxSessions, uptime)

	nameStyle := lipgloss.NewStyle().Foreground(tui.ColorText).Bold(true)
	metaStyle := lipgloss.NewStyle().Foreground(tui.ColorMuted)

	// Show agents inline to keep the header compact.
	if len(h.status.Agents) > 0 {
		var sb strings.Builder
		for i, a := range h.status.Agents {
			if i > 0 {
				sb.WriteString(metaStyle.Render(", "))
			}
			sb.WriteString(nameStyle.Render(a.Name) + " " + metaStyle.Render(a.Profile))
		}
		info += "\n" + metaStyle.Render("  Agents:  ") + sb.String()
	}

	headerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tui.ColorPrimary).
		Width(width - 2).
		Padding(0, 1)

	firstRow := lipgloss.JoinHorizontal(lipgloss.Top,
		left,
		lipgloss.NewStyle().Width(width-lipgloss.Width(left)-lipgloss.Width(right)-6).Render(""),
		right,
	)

	return headerStyle.Render(firstRow + "\n" + tui.Description.Render(info))
}

func (h headerModel) formatUptime() string {
	if h.status.StartedAt.IsZero() {
		return h.status.Uptime
	}
	d := time.Since(h.status.StartedAt)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
