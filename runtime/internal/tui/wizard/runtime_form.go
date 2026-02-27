package wizard

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/amurg-ai/amurg/runtime/internal/tui"
	"github.com/google/uuid"
)

type runtimeFormField int

const (
	rtFieldID runtimeFormField = iota
	rtFieldLogLevel
)

type runtimeFormModel struct {
	data *WizardData

	idInput       textinput.Model
	logLevelInput textinput.Model
	focused       runtimeFormField
}

func newRuntimeForm(data *WizardData) runtimeFormModel {
	id := textinput.New()
	id.Placeholder = "runtime-" + uuid.New().String()[:8]
	id.CharLimit = 128
	id.Width = 40

	ll := textinput.New()
	ll.Placeholder = "info"
	ll.CharLimit = 10
	ll.Width = 20

	return runtimeFormModel{
		data:          data,
		idInput:       id,
		logLevelInput: ll,
	}
}

// hasFixedID returns true when the runtime ID was assigned by device-code auth
// and should not be editable.
func (m runtimeFormModel) hasFixedID() bool {
	return m.data.RuntimeID != ""
}

func (m runtimeFormModel) Init() tea.Cmd {
	if m.hasFixedID() {
		// Device-code auth assigned the ID — skip to log level.
		m.focused = rtFieldLogLevel
		m.logLevelInput.Focus()
	} else {
		m.focused = rtFieldID
		m.idInput.Focus()
	}
	return textinput.Blink
}

func (m runtimeFormModel) Update(msg tea.Msg) (runtimeFormModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "down":
			return m.nextField()
		case "shift+tab", "up":
			return m.prevField()
		case "enter":
			if m.focused == rtFieldLogLevel {
				return m.finish()
			}
			return m.nextField()
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		}
	}

	var cmd tea.Cmd
	switch m.focused {
	case rtFieldID:
		if !m.hasFixedID() {
			m.idInput, cmd = m.idInput.Update(msg)
		}
	case rtFieldLogLevel:
		m.logLevelInput, cmd = m.logLevelInput.Update(msg)
	}
	return m, cmd
}

func (m runtimeFormModel) nextField() (runtimeFormModel, tea.Cmd) {
	m.idInput.Blur()
	m.logLevelInput.Blur()

	if m.focused < rtFieldLogLevel {
		m.focused++
	}
	// Skip ID field when it's fixed.
	if m.focused == rtFieldID && m.hasFixedID() {
		m.focused = rtFieldLogLevel
	}

	switch m.focused {
	case rtFieldID:
		m.idInput.Focus()
	case rtFieldLogLevel:
		m.logLevelInput.Focus()
	}
	return m, textinput.Blink
}

func (m runtimeFormModel) prevField() (runtimeFormModel, tea.Cmd) {
	m.idInput.Blur()
	m.logLevelInput.Blur()

	if m.focused > rtFieldID {
		m.focused--
	}
	// Skip ID field when it's fixed.
	if m.focused == rtFieldID && m.hasFixedID() {
		m.focused = rtFieldLogLevel
	}

	switch m.focused {
	case rtFieldID:
		m.idInput.Focus()
	case rtFieldLogLevel:
		m.logLevelInput.Focus()
	}
	return m, textinput.Blink
}

func (m runtimeFormModel) finish() (runtimeFormModel, tea.Cmd) {
	// Use device-code assigned ID, or user-entered/placeholder ID.
	if !m.hasFixedID() {
		rtID := m.idInput.Value()
		if rtID == "" {
			rtID = m.idInput.Placeholder
		}
		m.data.RuntimeID = rtID
	}

	ll := m.logLevelInput.Value()
	if ll == "" {
		ll = "info"
	}
	m.data.LogLevel = ll

	return m, func() tea.Msg { return stepCompleteMsg{} }
}

func (m runtimeFormModel) View() string {
	s := tui.Subtitle.Render("Runtime Settings") + "\n\n"

	if m.hasFixedID() {
		s += "  " + tui.Description.Render("Runtime ID: "+m.data.RuntimeID) + "\n"
		s += "  " + tui.Dimmed.Render("(assigned by device registration — not editable)") + "\n\n"
	} else {
		prefix := "  "
		if m.focused == rtFieldID {
			prefix = tui.Selected.Render("> ")
		}
		s += prefix + "Runtime ID:\n  " + m.idInput.View() + "\n"
	}

	prefix := "  "
	if m.focused == rtFieldLogLevel {
		prefix = tui.Selected.Render("> ")
	}
	s += prefix + "Log level (debug/info/warn/error):\n  " + m.logLevelInput.View() + "\n"

	s += "\n" + tui.Help.Render("  tab/↓ next • shift+tab/↑ prev • enter submit • esc back")
	return s
}
