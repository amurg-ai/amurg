package wizard

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

type hubFormModel struct {
	data     *WizardData
	choices  []string
	cursor   int
	urlInput textinput.Model
	editing  bool // true when typing custom URL
}

func newHubForm(data *WizardData) hubFormModel {
	ti := textinput.New()
	ti.Placeholder = "ws://localhost:8080/ws/runtime"
	ti.CharLimit = 256
	ti.Width = 50

	return hubFormModel{
		data:     data,
		choices:  []string{"Amurg Cloud (hub.amurg.ai)", "Self-hosted"},
		urlInput: ti,
	}
}

func (m hubFormModel) Init() tea.Cmd {
	return nil
}

func (m hubFormModel) Update(msg tea.Msg) (hubFormModel, tea.Cmd) {
	if m.editing {
		return m.updateEditing(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter":
			if m.cursor == 0 {
				// Amurg Cloud
				m.data.HubChoice = "cloud"
				m.data.HubURL = "wss://hub.amurg.ai/ws/runtime"
				return m, func() tea.Msg { return stepCompleteMsg{} }
			}
			// Self-hosted: show URL input
			m.editing = true
			m.urlInput.Focus()
			return m, textinput.Blink
		}
	}
	return m, nil
}

func (m hubFormModel) updateEditing(msg tea.Msg) (hubFormModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			url := m.urlInput.Value()
			if url == "" {
				url = "ws://localhost:8080/ws/runtime"
			}
			m.data.HubChoice = "self-hosted"
			m.data.HubURL = url
			return m, func() tea.Msg { return stepCompleteMsg{} }
		case "esc":
			m.editing = false
			m.urlInput.Blur()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.urlInput, cmd = m.urlInput.Update(msg)
	return m, cmd
}

func (m hubFormModel) View() string {
	s := tui.Subtitle.Render("Hub Connection") + "\n\n"
	s += "  Where is your Amurg Hub?\n\n"

	for i, choice := range m.choices {
		cursor := "  "
		style := tui.Dimmed
		if m.cursor == i {
			cursor = tui.Selected.Render("> ")
			style = tui.Selected
		}
		s += cursor + style.Render(choice) + "\n"
	}

	if m.editing {
		s += "\n  " + tui.Description.Render("Hub WebSocket URL:") + "\n"
		s += "  " + m.urlInput.View() + "\n"
	}

	s += "\n" + lipgloss.NewStyle().Foreground(tui.ColorMuted).Render("  ↑/↓ navigate • enter select")

	return s
}
