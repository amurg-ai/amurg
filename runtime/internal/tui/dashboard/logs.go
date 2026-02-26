package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

const maxLogLines = 1000

type logsModel struct {
	viewport   viewport.Model
	lines      []string
	autoScroll bool
	width      int
	height     int
}

func newLogs() logsModel {
	vp := viewport.New(80, 10)
	return logsModel{
		viewport:   vp,
		autoScroll: true,
	}
}

func (l *logsModel) SetSize(width, height int) {
	l.width = width
	l.height = height
	l.viewport.Width = width
	l.viewport.Height = height
}

func (l *logsModel) addEvent(msg EventMsg) {
	line := l.formatEvent(msg)
	l.lines = append(l.lines, line)

	// Trim old lines.
	if len(l.lines) > maxLogLines {
		l.lines = l.lines[len(l.lines)-maxLogLines:]
	}

	l.viewport.SetContent(strings.Join(l.lines, "\n"))
	if l.autoScroll {
		l.viewport.GotoBottom()
	}
}

func (l logsModel) formatEvent(msg EventMsg) string {
	ts := time.Now().Format("15:04:05")

	// Try to parse log entry data.
	var entry map[string]any
	if err := json.Unmarshal(msg.Data, &entry); err == nil {
		level, _ := entry["level"].(string)
		message, _ := entry["msg"].(string)

		// Build attrs string.
		var attrs []string
		for k, v := range entry {
			if k == "level" || k == "msg" || k == "time" {
				continue
			}
			attrs = append(attrs, fmt.Sprintf("%s=%v", k, v))
		}

		levelStyle := tui.LogLevelStyle(level)
		formatted := fmt.Sprintf("  %s %s  %s", ts, levelStyle.Render(fmt.Sprintf("%-5s", level)), message)
		if len(attrs) > 0 {
			formatted += "  " + tui.Dimmed.Render(strings.Join(attrs, " "))
		}
		return formatted
	}

	// Fallback: raw event.
	return fmt.Sprintf("  %s %s  %s", ts, tui.Dimmed.Render(msg.Type), string(msg.Data))
}

func (l logsModel) Update(msg tea.Msg) (logsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "G":
			l.autoScroll = true
			l.viewport.GotoBottom()
			return l, nil
		case "g":
			l.autoScroll = false
			l.viewport.GotoTop()
			return l, nil
		case "j", "down":
			l.autoScroll = false
		case "k", "up":
			l.autoScroll = false
		}
	}

	var cmd tea.Cmd
	l.viewport, cmd = l.viewport.Update(msg)
	return l, cmd
}

func (l logsModel) View() string {
	return l.viewport.View()
}
