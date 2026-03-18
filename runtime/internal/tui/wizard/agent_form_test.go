package wizard

import (
	"testing"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/permissionmode"
	tea "github.com/charmbracelet/bubbletea"
)

func TestAgentFormNextFieldFocusesWorkDir(t *testing.T) {
	m := newAgentForm(&WizardData{})
	m.selectingProfile = false
	m.profileCursor = profileIndex(t, protocol.ProfileClaudeCode)
	m.focusedField = fieldName
	m.nameInput.Focus()

	m, _ = m.nextField()

	if m.focusedField != fieldWorkDir {
		t.Fatalf("focusedField = %v, want %v", m.focusedField, fieldWorkDir)
	}
	if !m.dirInput.Focused() {
		t.Fatal("working directory input should be focused after advancing")
	}
	if m.nameInput.Focused() {
		t.Fatal("name input should be blurred after advancing")
	}
}

func TestAgentFormSkipsWorkDirForHTTPProfile(t *testing.T) {
	m := newAgentForm(&WizardData{})
	m.selectingProfile = false
	m.profileCursor = profileIndex(t, protocol.ProfileGenericHTTP)
	m.focusedField = fieldName
	m.nameInput.Focus()

	m, _ = m.nextField()

	if m.focusedField != fieldExtra1 {
		t.Fatalf("focusedField = %v, want %v", m.focusedField, fieldExtra1)
	}
	if !m.extra1Input.Focused() {
		t.Fatal("base URL input should be focused after advancing")
	}
	if m.nameInput.Focused() {
		t.Fatal("name input should be blurred after advancing")
	}
}

func TestAgentFormBuildsCodexTMuxTransport(t *testing.T) {
	m := newAgentForm(&WizardData{})
	m.selectingProfile = false
	m.profileCursor = profileIndex(t, protocol.ProfileCodex)
	m.nameInput.SetValue("Codex")
	m.dirInput.SetValue(t.TempDir())
	m.extra1Input.SetValue("gpt-5.4")
	m.extra2Input.SetValue("tmux")

	agent := m.buildAgent(protocol.ProfileCodex)
	if agent.Codex == nil {
		t.Fatal("expected codex config")
	}
	if agent.Codex.Transport != "tmux" {
		t.Fatalf("transport = %q, want tmux", agent.Codex.Transport)
	}
}

func TestAgentFormCyclesClaudePermissionMode(t *testing.T) {
	m := newAgentForm(&WizardData{})
	m.selectingProfile = false
	m.profileCursor = profileIndex(t, protocol.ProfileClaudeCode)
	m.focusedField = fieldExtra2

	m, _ = m.updateFields(tea.KeyMsg{Type: tea.KeyRight})

	if got := permissionmode.ClaudeWizardOptions[m.claudePermissionIx].Value; got != "auto" {
		t.Fatalf("permission mode = %q, want auto", got)
	}
}

func TestAgentFormBuildsClaudePermissionMode(t *testing.T) {
	m := newAgentForm(&WizardData{})
	m.selectingProfile = false
	m.profileCursor = profileIndex(t, protocol.ProfileClaudeCode)
	m.nameInput.SetValue("Claude")
	m.dirInput.SetValue(t.TempDir())
	_, idx := permissionmode.ClaudeWizardOptionByValue("skip")
	m.claudePermissionIx = idx

	agent := m.buildAgent(protocol.ProfileClaudeCode)
	if agent.ClaudeCode == nil {
		t.Fatal("expected claude code config")
	}
	if agent.ClaudeCode.PermissionMode != "skip" {
		t.Fatalf("permission mode = %q, want skip", agent.ClaudeCode.PermissionMode)
	}
}

func profileIndex(t *testing.T, profile string) int {
	t.Helper()
	for i, option := range profileOptions {
		if option.profile == profile {
			return i
		}
	}
	t.Fatalf("profile %q not found", profile)
	return -1
}
