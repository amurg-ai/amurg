package wizard

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHubFormSelfHostedNormalizesHTTPURL(t *testing.T) {
	data := &WizardData{}
	m := newHubForm(data)
	m.cursor = 1

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected blink command when entering self-hosted edit mode")
	}
	m = updated
	m.urlInput.SetValue("http://localhost:8090")

	m, cmd = m.updateEditing(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected step completion command")
	}
	if data.HubBaseURL != "http://localhost:8090" {
		t.Fatalf("HubBaseURL = %q, want %q", data.HubBaseURL, "http://localhost:8090")
	}
	if data.HubURL != "ws://localhost:8090/ws/runtime" {
		t.Fatalf("HubURL = %q, want %q", data.HubURL, "ws://localhost:8090/ws/runtime")
	}
}

func TestHubFormRejectsInvalidURL(t *testing.T) {
	data := &WizardData{}
	m := newHubForm(data)
	m.cursor = 1
	m.editing = true
	m.urlInput.SetValue("http://localhost:8080?bad=1")

	m, cmd := m.updateEditing(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected no completion command for invalid URL")
	}
	if m.errMsg == "" {
		t.Fatal("expected validation error")
	}
}
