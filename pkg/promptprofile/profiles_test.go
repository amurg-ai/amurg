package promptprofile

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	if got := Normalize(""); got != DefaultID {
		t.Fatalf("expected default id %q, got %q", DefaultID, got)
	}
	if got := Normalize(" deep_debug "); got != "deep_debug" {
		t.Fatalf("expected trimmed id, got %q", got)
	}
}

func TestAppend(t *testing.T) {
	combined := Append("Base prompt.", "careful")
	if !strings.Contains(combined, "This is a sensitive operation.") {
		t.Fatalf("expected combined prompt to include profile text, got %q", combined)
	}
	if !strings.HasSuffix(combined, "Base prompt.") {
		t.Fatalf("expected base prompt to remain last, got %q", combined)
	}
}
