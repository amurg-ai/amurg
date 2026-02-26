// Package wizard provides a bubbletea-based TUI wizard for runtime configuration.
package wizard

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

// step enumerates the wizard steps.
type step int

const (
	stepHub step = iota
	stepAuth
	stepDeviceCode
	stepAgent
	stepRuntime
	stepConfirm
)

// WizardData collects all configuration from the wizard steps.
type WizardData struct {
	HubChoice string // "cloud" or "self-hosted"
	HubURL    string

	AuthChoice string // "device-code" or "manual"
	Token      string
	RuntimeID  string
	OrgID      string

	Agents []config.AgentConfig

	RuntimeIDOverride string // user override (empty = use auth-provided)
	LogLevel          string

	OutputPath     string
	GenerateSystemd bool
}

// Result is returned when the wizard completes.
type Result struct {
	Config    *config.Config
	Path      string
	StartNow  bool
	Cancelled bool
}

// Model is the root wizard model.
type Model struct {
	step   step
	data   *WizardData
	width  int
	height int

	hub        hubFormModel
	auth       authFormModel
	deviceCode deviceCodeModel
	agent      agentFormModel
	runtime    runtimeFormModel
	confirm    confirmModel

	result Result
	done   bool
}

// NewModel creates a new wizard model.
func NewModel(outputPath string, generateSystemd bool) Model {
	data := &WizardData{
		OutputPath:      outputPath,
		GenerateSystemd: generateSystemd,
		LogLevel:        "info",
	}

	return Model{
		step:       stepHub,
		data:       data,
		hub:        newHubForm(data),
		auth:       newAuthForm(data),
		deviceCode: newDeviceCodeModel(data),
		agent:      newAgentForm(data),
		runtime:    newRuntimeForm(data),
		confirm:    newConfirmModel(data),
	}
}

// stepCompleteMsg signals the current step is done and we should advance.
type stepCompleteMsg struct{}

// stepBackMsg signals we should go back one step.
type stepBackMsg struct{}

// wizardDoneMsg signals the wizard is finished (confirm wrote config).
type wizardDoneMsg struct {
	result Result
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return m.hub.Init()
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))):
			m.result.Cancelled = true
			m.done = true
			return m, tea.Quit
		}

	case stepCompleteMsg:
		return m.advance()

	case stepBackMsg:
		return m.goBack()

	case wizardDoneMsg:
		m.result = msg.result
		m.done = true
		return m, tea.Quit
	}

	// Delegate to current step.
	var cmd tea.Cmd
	switch m.step {
	case stepHub:
		m.hub, cmd = m.hub.Update(msg)
	case stepAuth:
		m.auth, cmd = m.auth.Update(msg)
	case stepDeviceCode:
		m.deviceCode, cmd = m.deviceCode.Update(msg)
	case stepAgent:
		m.agent, cmd = m.agent.Update(msg)
	case stepRuntime:
		m.runtime, cmd = m.runtime.Update(msg)
	case stepConfirm:
		m.confirm, cmd = m.confirm.Update(msg)
	}
	return m, cmd
}

// advance moves to the next step.
func (m Model) advance() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepHub:
		m.step = stepAuth
		return m, m.auth.Init()
	case stepAuth:
		if m.data.AuthChoice == "device-code" {
			m.step = stepDeviceCode
			return m, m.deviceCode.Init()
		}
		m.step = stepAgent
		return m, m.agent.Init()
	case stepDeviceCode:
		m.step = stepAgent
		return m, m.agent.Init()
	case stepAgent:
		m.step = stepRuntime
		return m, m.runtime.Init()
	case stepRuntime:
		m.step = stepConfirm
		return m, m.confirm.Init()
	case stepConfirm:
		// handled by wizardDoneMsg
	}
	return m, nil
}

// goBack moves to the previous step.
func (m Model) goBack() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepAuth:
		m.step = stepHub
		return m, m.hub.Init()
	case stepDeviceCode:
		m.step = stepAuth
		return m, m.auth.Init()
	case stepAgent:
		if m.data.AuthChoice == "device-code" {
			m.step = stepDeviceCode
			return m, m.deviceCode.Init()
		}
		m.step = stepAuth
		return m, m.auth.Init()
	case stepRuntime:
		m.step = stepAgent
		return m, m.agent.Init()
	case stepConfirm:
		m.step = stepRuntime
		return m, m.runtime.Init()
	}
	return m, nil
}

// View renders the current step.
func (m Model) View() string {
	header := tui.Title.Render("Amurg Runtime — Configuration Wizard")
	progress := m.progressBar()

	var body string
	switch m.step {
	case stepHub:
		body = m.hub.View()
	case stepAuth:
		body = m.auth.View()
	case stepDeviceCode:
		body = m.deviceCode.View()
	case stepAgent:
		body = m.agent.View()
	case stepRuntime:
		body = m.runtime.View()
	case stepConfirm:
		body = m.confirm.View()
	}

	help := tui.Help.Render("ctrl+c quit • esc back")

	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		header,
		progress,
		"",
		body,
		"",
		help,
	)
}

// Done returns whether the wizard has completed.
func (m Model) Done() bool { return m.done }

// Result returns the wizard result.
func (m Model) Result() Result { return m.result }

// progressBar renders a simple step indicator.
func (m Model) progressBar() string {
	steps := []string{"Hub", "Auth", "Agents", "Runtime", "Confirm"}
	current := 0
	switch m.step {
	case stepHub:
		current = 0
	case stepAuth, stepDeviceCode:
		current = 1
	case stepAgent:
		current = 2
	case stepRuntime:
		current = 3
	case stepConfirm:
		current = 4
	}

	var parts []string
	for i, name := range steps {
		if i == current {
			parts = append(parts, tui.Selected.Render("● "+name))
		} else if i < current {
			parts = append(parts, tui.Success.Render("✓ "+name))
		} else {
			parts = append(parts, tui.Dimmed.Render("○ "+name))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, joinWithSep(parts, "  ")...)
}

func joinWithSep(parts []string, sep string) []string {
	if len(parts) == 0 {
		return nil
	}
	result := make([]string, 0, len(parts)*2-1)
	for i, p := range parts {
		if i > 0 {
			result = append(result, sep)
		}
		result = append(result, p)
	}
	return result
}
