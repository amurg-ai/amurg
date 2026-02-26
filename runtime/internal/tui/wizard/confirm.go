package wizard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/tui"
	"github.com/amurg-ai/amurg/runtime/internal/wizard"
)

type confirmAction int

const (
	actionWrite confirmAction = iota
	actionEdit
	actionCancel
)

type confirmModel struct {
	data    *WizardData
	cursor  int
	actions []string
	err     string
}

func newConfirmModel(data *WizardData) confirmModel {
	return confirmModel{
		data:    data,
		actions: []string{"Write config and start", "Write config", "Cancel"},
	}
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (confirmModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.actions)-1 {
				m.cursor++
			}
		case "enter":
			return m.execute()
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		}
	}
	return m, nil
}

func (m confirmModel) execute() (confirmModel, tea.Cmd) {
	switch confirmAction(m.cursor) {
	case actionWrite, actionEdit:
		cfg := m.buildConfig()
		path, err := m.writeConfig(cfg)
		if err != nil {
			m.err = err.Error()
			return m, nil
		}

		startNow := confirmAction(m.cursor) == actionWrite
		return m, func() tea.Msg {
			return wizardDoneMsg{result: Result{
				Config:   cfg,
				Path:     path,
				StartNow: startNow,
			}}
		}

	case actionCancel:
		return m, func() tea.Msg {
			return wizardDoneMsg{result: Result{Cancelled: true}}
		}
	}
	return m, nil
}

func (m confirmModel) buildConfig() *config.Config {
	cfg := &config.Config{}

	cfg.Hub.URL = m.data.HubURL
	cfg.Hub.Token = m.data.Token

	rtID := m.data.RuntimeIDOverride
	if rtID == "" {
		rtID = m.data.RuntimeID
	}
	cfg.Runtime.ID = rtID
	cfg.Runtime.OrgID = m.data.OrgID
	cfg.Runtime.LogLevel = m.data.LogLevel
	cfg.Agents = m.data.Agents

	return cfg
}

func (m confirmModel) writeConfig(cfg *config.Config) (string, error) {
	outputPath := m.data.OutputPath
	if outputPath == "" {
		outputPath = wizard.DefaultConfigPath()
	}

	if dir := filepath.Dir(outputPath); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", fmt.Errorf("create config directory: %w", err)
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(outputPath, append(data, '\n'), 0600); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}

	return outputPath, nil
}

func (m confirmModel) View() string {
	s := tui.Subtitle.Render("Configuration Summary") + "\n\n"

	// Summary
	s += renderRow("Hub", m.data.HubURL)
	s += renderRow("Auth", m.authSummary())

	rtID := m.data.RuntimeIDOverride
	if rtID == "" {
		rtID = m.data.RuntimeID
	}
	s += renderRow("Runtime ID", rtID)
	s += renderRow("Log level", m.data.LogLevel)
	s += renderRow("Agents", fmt.Sprintf("%d configured", len(m.data.Agents)))

	for i, a := range m.data.Agents {
		s += fmt.Sprintf("    %d. %s (%s)\n", i+1, a.Name, a.Profile)
	}

	outputPath := m.data.OutputPath
	if outputPath == "" {
		outputPath = wizard.DefaultConfigPath()
	}
	s += "\n" + renderRow("Output", outputPath)

	if m.err != "" {
		s += "\n  " + tui.ErrorStyle.Render("Error: "+m.err) + "\n"
	}

	s += "\n"
	for i, action := range m.actions {
		cursor := "  "
		style := tui.Dimmed
		if m.cursor == i {
			cursor = tui.Selected.Render("> ")
			style = tui.Selected
		}
		s += cursor + style.Render(action) + "\n"
	}

	s += "\n" + tui.Help.Render("  ↑/↓ navigate • enter select • esc back")
	return s
}

func (m confirmModel) authSummary() string {
	if m.data.AuthChoice == "device-code" {
		return "Device registration"
	}
	return "Manual token"
}

func renderRow(label, value string) string {
	labelStyle := lipgloss.NewStyle().
		Foreground(tui.ColorSubtle).
		Width(14)
	return "  " + labelStyle.Render(label) + value + "\n"
}

