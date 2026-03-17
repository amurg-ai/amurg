package wizard

import (
	"testing"

	"github.com/amurg-ai/amurg/pkg/protocol"
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
