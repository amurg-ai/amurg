package wizard

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

var profileOptions = []struct {
	profile string
	label   string
}{
	{protocol.ProfileClaudeCode, "Claude Code (Anthropic CLI agent)"},
	{protocol.ProfileGitHubCopilot, "GitHub Copilot (gh copilot)"},
	{protocol.ProfileCodex, "Codex (OpenAI CLI agent)"},
	{protocol.ProfileKilo, "Kilo Code (open-source agent)"},
	{protocol.ProfileGenericCLI, "Generic CLI (any interactive command)"},
	{protocol.ProfileGenericJob, "Generic Job (run-to-completion command)"},
	{protocol.ProfileGenericHTTP, "Generic HTTP (forward to URL)"},
	{protocol.ProfileExternal, "External (JSON-Lines stdio protocol)"},
}

// agentField tracks which field in the agent form is focused.
type agentField int

const (
	fieldProfile agentField = iota
	fieldName
	fieldWorkDir
	fieldExtra1 // model, command, or base URL
	fieldExtra2 // permission mode, args, provider
)

type agentFormModel struct {
	data *WizardData

	// Profile selection
	profileCursor int
	selectingProfile bool

	// Current agent being configured
	agentIndex  int
	nameInput   textinput.Model
	dirInput    textinput.Model
	extra1Input textinput.Model
	extra2Input textinput.Model

	focusedField agentField
	dirError     string
	agents       []config.AgentConfig
}

func newAgentForm(data *WizardData) agentFormModel {
	name := textinput.New()
	name.Placeholder = "Agent name"
	name.CharLimit = 128
	name.Width = 50

	dir := textinput.New()
	dir.CharLimit = 256
	dir.Width = 50

	extra1 := textinput.New()
	extra1.CharLimit = 256
	extra1.Width = 50

	extra2 := textinput.New()
	extra2.CharLimit = 256
	extra2.Width = 50

	return agentFormModel{
		data:             data,
		selectingProfile: true,
		nameInput:        name,
		dirInput:         dir,
		extra1Input:      extra1,
		extra2Input:      extra2,
	}
}

func (m agentFormModel) Init() tea.Cmd {
	return nil
}

func (m agentFormModel) Update(msg tea.Msg) (agentFormModel, tea.Cmd) {
	if m.selectingProfile {
		return m.updateProfileSelect(msg)
	}
	return m.updateFields(msg)
}

func (m agentFormModel) updateProfileSelect(msg tea.Msg) (agentFormModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.profileCursor > 0 {
				m.profileCursor--
			}
		case "down", "j":
			if m.profileCursor < len(profileOptions)-1 {
				m.profileCursor++
			}
		case "enter":
			m.selectingProfile = false
			m.focusedField = fieldName
			selected := profileOptions[m.profileCursor]
			m.nameInput.SetValue("")
			m.nameInput.Placeholder = selected.label
			m.dirInput.SetValue("")
			m.extra1Input.SetValue("")
			m.extra2Input.SetValue("")
			m.dirError = ""
			m.setupFieldsForProfile(selected.profile)
			m.nameInput.Focus()
			return m, textinput.Blink
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		}
	}
	return m, nil
}

func (m *agentFormModel) setupFieldsForProfile(profile string) {
	wd, _ := os.Getwd()
	if wd == "" {
		if home, err := os.UserHomeDir(); err == nil {
			wd = home
		}
	}

	switch profile {
	case protocol.ProfileClaudeCode, protocol.ProfileGitHubCopilot, protocol.ProfileCodex, protocol.ProfileKilo:
		m.dirInput.Placeholder = wd
		m.extra1Input.Placeholder = "leave empty for default"
		switch profile {
		case protocol.ProfileKilo:
			m.extra2Input.Placeholder = "provider (leave empty for default)"
		case protocol.ProfileClaudeCode:
			m.extra2Input.Placeholder = "permission mode (leave empty for default)"
		}
	case protocol.ProfileGenericCLI, protocol.ProfileGenericJob, protocol.ProfileExternal:
		m.extra1Input.Placeholder = "command"
		m.extra2Input.Placeholder = "arguments (space-separated)"
	case protocol.ProfileGenericHTTP:
		m.extra1Input.Placeholder = "base URL (e.g. http://localhost:8000)"
	}
}

func (m agentFormModel) updateFields(msg tea.Msg) (agentFormModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "down":
			return m.nextField()
		case "shift+tab", "up":
			return m.prevField()
		case "enter":
			if m.focusedField == m.lastField() {
				return m.finishAgent()
			}
			return m.nextField()
		case "esc":
			m.selectingProfile = true
			return m, nil
		}
	}

	// Update the focused input.
	var cmd tea.Cmd
	switch m.focusedField {
	case fieldName:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case fieldWorkDir:
		m.dirInput, cmd = m.dirInput.Update(msg)
		m.dirError = "" // clear on typing
	case fieldExtra1:
		m.extra1Input, cmd = m.extra1Input.Update(msg)
	case fieldExtra2:
		m.extra2Input, cmd = m.extra2Input.Update(msg)
	}
	return m, cmd
}

func (m agentFormModel) lastField() agentField {
	profile := profileOptions[m.profileCursor].profile
	switch profile {
	case protocol.ProfileClaudeCode, protocol.ProfileKilo:
		return fieldExtra2
	case protocol.ProfileGitHubCopilot, protocol.ProfileCodex:
		return fieldExtra1
	case protocol.ProfileGenericCLI, protocol.ProfileGenericJob, protocol.ProfileExternal:
		return fieldExtra2
	case protocol.ProfileGenericHTTP:
		return fieldExtra1
	}
	return fieldName
}

func (m agentFormModel) hasWorkDir() bool {
	profile := profileOptions[m.profileCursor].profile
	switch profile {
	case protocol.ProfileClaudeCode, protocol.ProfileGitHubCopilot, protocol.ProfileCodex, protocol.ProfileKilo:
		return true
	}
	return false
}

func (m agentFormModel) nextField() (agentFormModel, tea.Cmd) {
	m.blurAll()
	next := m.focusedField + 1

	// Skip workdir for profiles that don't have it.
	if next == fieldWorkDir && !m.hasWorkDir() {
		next = fieldExtra1
	}

	if next > m.lastField() {
		next = m.lastField()
	}

	m.focusedField = next
	return m, m.focusCurrent()
}

func (m agentFormModel) prevField() (agentFormModel, tea.Cmd) {
	m.blurAll()
	prev := m.focusedField - 1

	if prev == fieldWorkDir && !m.hasWorkDir() {
		prev = fieldName
	}
	if prev < fieldName {
		prev = fieldName
	}

	m.focusedField = prev
	return m, m.focusCurrent()
}

func (m *agentFormModel) blurAll() {
	m.nameInput.Blur()
	m.dirInput.Blur()
	m.extra1Input.Blur()
	m.extra2Input.Blur()
}

func (m agentFormModel) focusCurrent() tea.Cmd {
	switch m.focusedField {
	case fieldName:
		m.nameInput.Focus()
	case fieldWorkDir:
		m.dirInput.Focus()
	case fieldExtra1:
		m.extra1Input.Focus()
	case fieldExtra2:
		m.extra2Input.Focus()
	}
	return textinput.Blink
}

func (m agentFormModel) finishAgent() (agentFormModel, tea.Cmd) {
	profile := profileOptions[m.profileCursor].profile

	// Validate directory if needed.
	if m.hasWorkDir() {
		dir := m.dirInput.Value()
		if dir == "" {
			dir = m.dirInput.Placeholder
		}
		dir = expandHome(dir)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			m.dirError = "Directory does not exist or is not a directory"
			m.focusedField = fieldWorkDir
			return m, m.focusCurrent()
		}
	}

	agent := m.buildAgent(profile)
	m.agents = append(m.agents, agent)
	m.agentIndex++

	// Store collected agents.
	m.data.Agents = m.agents

	// Move to next step.
	return m, func() tea.Msg { return stepCompleteMsg{} }
}

func (m agentFormModel) buildAgent(profile string) config.AgentConfig {
	name := m.nameInput.Value()
	if name == "" {
		name = profileOptions[m.profileCursor].label
	}

	agent := config.AgentConfig{
		ID:      fmt.Sprintf("%s-%d", profile, m.agentIndex+1),
		Name:    name,
		Profile: profile,
	}

	workDir := m.dirInput.Value()
	if workDir == "" {
		workDir = m.dirInput.Placeholder
	}
	workDir = expandHome(workDir)

	switch profile {
	case protocol.ProfileClaudeCode:
		cc := &config.ClaudeCodeConfig{WorkDir: workDir}
		if v := m.extra1Input.Value(); v != "" {
			cc.Model = v
		}
		if v := m.extra2Input.Value(); v != "" {
			cc.PermissionMode = v
		}
		agent.ClaudeCode = cc

	case protocol.ProfileGitHubCopilot:
		cp := &config.CopilotConfig{WorkDir: workDir}
		if v := m.extra1Input.Value(); v != "" {
			cp.Model = v
		}
		agent.Copilot = cp

	case protocol.ProfileCodex:
		cx := &config.CodexConfig{WorkDir: workDir}
		if v := m.extra1Input.Value(); v != "" {
			cx.Model = v
		}
		agent.Codex = cx

	case protocol.ProfileKilo:
		kc := &config.KiloConfig{WorkDir: workDir}
		if v := m.extra1Input.Value(); v != "" {
			kc.Model = v
		}
		if v := m.extra2Input.Value(); v != "" {
			kc.Provider = v
		}
		agent.Kilo = kc

	case protocol.ProfileGenericCLI:
		agent.CLI = &config.CLIConfig{
			Command: m.extra1Input.Value(),
			Args:    splitArgs(m.extra2Input.Value()),
		}

	case protocol.ProfileGenericJob:
		agent.Job = &config.JobConfig{
			Command: m.extra1Input.Value(),
			Args:    splitArgs(m.extra2Input.Value()),
		}

	case protocol.ProfileGenericHTTP:
		agent.HTTP = &config.HTTPConfig{
			BaseURL: m.extra1Input.Value(),
		}

	case protocol.ProfileExternal:
		agent.External = &config.ExternalConfig{
			Command: m.extra1Input.Value(),
			Args:    splitArgs(m.extra2Input.Value()),
		}
	}

	return agent
}

func (m agentFormModel) View() string {
	s := tui.Subtitle.Render(fmt.Sprintf("Agent %d Configuration", m.agentIndex+1)) + "\n\n"

	if m.selectingProfile {
		s += "  Select agent profile:\n\n"
		for i, opt := range profileOptions {
			cursor := "  "
			style := tui.Dimmed
			if m.profileCursor == i {
				cursor = tui.Selected.Render("> ")
				style = tui.Selected
			}
			s += cursor + style.Render(opt.label) + "\n"
		}
		s += "\n" + tui.Help.Render("  ↑/↓ navigate • enter select • esc back")
		return s
	}

	profile := profileOptions[m.profileCursor].profile
	s += "  " + tui.Description.Render("Profile: "+profile) + "\n\n"

	s += m.renderField("  Name", m.nameInput, fieldName)

	if m.hasWorkDir() {
		s += m.renderField("  Working directory", m.dirInput, fieldWorkDir)
		if m.dirError != "" {
			s += "  " + tui.ErrorStyle.Render("  "+m.dirError) + "\n"
		}
	}

	switch profile {
	case protocol.ProfileClaudeCode:
		s += m.renderField("  Model", m.extra1Input, fieldExtra1)
		s += m.renderField("  Permission mode", m.extra2Input, fieldExtra2)
	case protocol.ProfileGitHubCopilot, protocol.ProfileCodex:
		s += m.renderField("  Model", m.extra1Input, fieldExtra1)
	case protocol.ProfileKilo:
		s += m.renderField("  Model", m.extra1Input, fieldExtra1)
		s += m.renderField("  Provider", m.extra2Input, fieldExtra2)
	case protocol.ProfileGenericCLI, protocol.ProfileGenericJob, protocol.ProfileExternal:
		s += m.renderField("  Command", m.extra1Input, fieldExtra1)
		s += m.renderField("  Arguments", m.extra2Input, fieldExtra2)
	case protocol.ProfileGenericHTTP:
		s += m.renderField("  Base URL", m.extra1Input, fieldExtra1)
	}

	s += "\n" + tui.Help.Render("  tab/↓ next • shift+tab/↑ prev • enter submit • esc back")
	return s
}

func (m agentFormModel) renderField(label string, input textinput.Model, field agentField) string {
	prefix := "  "
	if m.focusedField == field {
		prefix = tui.Selected.Render("> ")
	}
	return prefix + lipgloss.NewStyle().Foreground(tui.ColorText).Render(label+":") + "\n  " + input.View() + "\n"
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

func splitArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}
