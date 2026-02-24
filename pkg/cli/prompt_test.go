package cli

import (
	"bytes"
	"strings"
	"testing"
)

func newTestPrompter(input string) (*Prompter, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &Prompter{
		In:  strings.NewReader(input),
		Out: out,
	}, out
}

func TestAsk_WithInput(t *testing.T) {
	p, _ := newTestPrompter("hello\n")
	got := p.Ask("Name", "default")
	if got != "hello" {
		t.Errorf("Ask() = %q, want %q", got, "hello")
	}
}

func TestAsk_EmptyUsesDefault(t *testing.T) {
	p, _ := newTestPrompter("\n")
	got := p.Ask("Name", "fallback")
	if got != "fallback" {
		t.Errorf("Ask() = %q, want %q", got, "fallback")
	}
}

func TestAsk_WhitespaceUsesDefault(t *testing.T) {
	p, _ := newTestPrompter("   \n")
	got := p.Ask("Name", "fallback")
	if got != "fallback" {
		t.Errorf("Ask() = %q, want %q", got, "fallback")
	}
}

func TestAskPassword_Fallback(t *testing.T) {
	// Not a real terminal, so it falls back to plain read.
	p, _ := newTestPrompter("secret123\n")
	got := p.AskPassword("Password")
	if got != "secret123" {
		t.Errorf("AskPassword() = %q, want %q", got, "secret123")
	}
}

func TestAskInt_ValidInput(t *testing.T) {
	p, _ := newTestPrompter("5\n")
	got := p.AskInt("Count", 1)
	if got != 5 {
		t.Errorf("AskInt() = %d, want %d", got, 5)
	}
}

func TestAskInt_DefaultOnEmpty(t *testing.T) {
	p, _ := newTestPrompter("\n")
	got := p.AskInt("Count", 3)
	if got != 3 {
		t.Errorf("AskInt() = %d, want %d", got, 3)
	}
}

func TestChoose_Selection(t *testing.T) {
	p, _ := newTestPrompter("2\n")
	options := []string{"alpha", "beta", "gamma"}
	got := p.Choose("Pick one", options, 0)
	if got != "beta" {
		t.Errorf("Choose() = %q, want %q", got, "beta")
	}
}

func TestChoose_DefaultOnEmpty(t *testing.T) {
	p, _ := newTestPrompter("\n")
	options := []string{"alpha", "beta", "gamma"}
	got := p.Choose("Pick one", options, 1)
	if got != "beta" {
		t.Errorf("Choose() = %q, want %q", got, "beta")
	}
}

func TestConfirm_Yes(t *testing.T) {
	p, _ := newTestPrompter("y\n")
	got := p.Confirm("Continue?", false)
	if !got {
		t.Error("Confirm() = false, want true")
	}
}

func TestConfirm_No(t *testing.T) {
	p, _ := newTestPrompter("n\n")
	got := p.Confirm("Continue?", true)
	if got {
		t.Error("Confirm() = true, want false")
	}
}

func TestConfirm_DefaultYes(t *testing.T) {
	p, _ := newTestPrompter("\n")
	got := p.Confirm("Continue?", true)
	if !got {
		t.Error("Confirm() = false, want true (default)")
	}
}

func TestConfirm_DefaultNo(t *testing.T) {
	p, _ := newTestPrompter("\n")
	got := p.Confirm("Continue?", false)
	if got {
		t.Error("Confirm() = true, want false (default)")
	}
}
