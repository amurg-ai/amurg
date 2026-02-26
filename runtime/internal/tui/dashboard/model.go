package dashboard

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amurg-ai/amurg/runtime/internal/ipc"
	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

// Panel identifies which dashboard panel is focused.
type Panel int

const (
	PanelSessions Panel = iota
	PanelLogs
)

// Model is the root dashboard TUI model.
type Model struct {
	header   headerModel
	sessions sessionsModel
	logs     logsModel
	help     helpModel

	activePanel Panel
	width       int
	height      int
	detached    bool
	quitting    bool
}

// NewModel creates a dashboard model for attached mode (via IPC status).
func NewModel(status ipc.StatusResult, sessions []ipc.SessionInfo) Model {
	return Model{
		header:   newHeader(status),
		sessions: newSessions(sessions),
		logs:     newLogs(),
		help:     newHelp(),
	}
}

// DetachMsg signals the TUI should detach (leave daemon running).
type DetachMsg struct{}

// EventMsg wraps an event from IPC or event bus.
type EventMsg struct {
	Type string
	Data []byte
}

// StatusUpdateMsg carries fresh status data.
type StatusUpdateMsg struct {
	Status ipc.StatusResult
}

// SessionsUpdateMsg carries fresh session data.
type SessionsUpdateMsg struct {
	Sessions []ipc.SessionInfo
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.logs.SetSize(msg.Width-4, m.logsHeight())
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c", "q"))):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+d", "d"))):
			m.detached = true
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			if m.activePanel == PanelSessions {
				m.activePanel = PanelLogs
			} else {
				m.activePanel = PanelSessions
			}
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("?"))):
			m.help.toggle()
			return m, nil
		}

	case StatusUpdateMsg:
		m.header.update(msg.Status)
		return m, nil

	case SessionsUpdateMsg:
		m.sessions.update(msg.Sessions)
		return m, nil

	case EventMsg:
		m.logs.addEvent(msg)
		return m, nil
	}

	// Delegate to active panel.
	var cmd tea.Cmd
	switch m.activePanel {
	case PanelSessions:
		m.sessions, cmd = m.sessions.Update(msg)
	case PanelLogs:
		m.logs, cmd = m.logs.Update(msg)
	}
	return m, cmd
}

func (m Model) View() string {
	if m.help.visible {
		return m.help.View()
	}

	headerView := m.header.View(m.width)

	sessionsBorder := lipgloss.RoundedBorder()
	logsBorder := lipgloss.RoundedBorder()

	sessStyle := lipgloss.NewStyle().
		Border(sessionsBorder).
		BorderForeground(tui.ColorMuted).
		Width(m.width - 2)

	logsStyle := lipgloss.NewStyle().
		Border(logsBorder).
		BorderForeground(tui.ColorMuted).
		Width(m.width - 2)

	if m.activePanel == PanelSessions {
		sessStyle = sessStyle.BorderForeground(tui.ColorPrimary)
	} else {
		logsStyle = logsStyle.BorderForeground(tui.ColorPrimary)
	}

	sessView := sessStyle.Render(
		tui.Subtitle.Render(" Sessions") + "\n" + m.sessions.View(),
	)
	logsView := logsStyle.Render(
		tui.Subtitle.Render(" Logs") + "\n" + m.logs.View(),
	)

	helpBar := m.help.bar()

	return lipgloss.JoinVertical(lipgloss.Left,
		headerView,
		sessView,
		logsView,
		helpBar,
	)
}

// Detached returns true if the user pressed detach.
func (m Model) Detached() bool { return m.detached }

// Quitting returns true if the user quit.
func (m Model) Quitting() bool { return m.quitting }

func (m Model) logsHeight() int {
	// Reserve space for header, sessions, help bar, borders.
	used := 6 + m.sessions.height() + 4
	h := m.height - used
	if h < 5 {
		h = 5
	}
	return h
}
