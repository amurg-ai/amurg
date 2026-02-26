package wizard

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

type authFormModel struct {
	data       *WizardData
	choices    []string
	cursor     int
	tokenInput textinput.Model
	editing    bool // typing manual token
}

func newAuthForm(data *WizardData) authFormModel {
	ti := textinput.New()
	ti.Placeholder = "paste your token here"
	ti.CharLimit = 512
	ti.Width = 60
	ti.EchoMode = textinput.EchoPassword

	return authFormModel{
		data:       data,
		choices:    []string{"Register via browser (recommended)", "Enter token manually"},
		tokenInput: ti,
	}
}

func (m authFormModel) Init() tea.Cmd { return nil }

func (m authFormModel) Update(msg tea.Msg) (authFormModel, tea.Cmd) {
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
				m.data.AuthChoice = "device-code"
				return m, func() tea.Msg { return stepCompleteMsg{} }
			}
			m.editing = true
			m.tokenInput.Focus()
			return m, textinput.Blink
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		}
	}
	return m, nil
}

func (m authFormModel) updateEditing(msg tea.Msg) (authFormModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			token := m.tokenInput.Value()
			if token == "" {
				return m, nil
			}
			m.data.AuthChoice = "manual"
			m.data.Token = token
			return m, func() tea.Msg { return stepCompleteMsg{} }
		case "esc":
			m.editing = false
			m.tokenInput.Blur()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.tokenInput, cmd = m.tokenInput.Update(msg)
	return m, cmd
}

func (m authFormModel) View() string {
	s := tui.Subtitle.Render("Authentication") + "\n\n"
	s += "  How would you like to authenticate?\n\n"

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
		s += "\n  " + tui.Description.Render("Authentication token:") + "\n"
		s += "  " + m.tokenInput.View() + "\n"
	}

	s += "\n" + lipgloss.NewStyle().Foreground(tui.ColorMuted).Render("  ↑/↓ navigate • enter select • esc back")

	return s
}
