package permissionmode

import "testing"

func TestClaudeWizardOptionByValue(t *testing.T) {
	option, idx := ClaudeWizardOptionByValue("skip")
	if idx < 0 || idx >= len(ClaudeWizardOptions) {
		t.Fatalf("index out of range: %d", idx)
	}
	if option.Value != "skip" {
		t.Fatalf("value = %q, want skip", option.Value)
	}
}
