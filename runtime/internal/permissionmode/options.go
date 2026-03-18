package permissionmode

type Option struct {
	Value       string
	Label       string
	Description string
}

var ClaudeWizardOptions = []Option{
	{
		Value:       "",
		Label:       "Claude Default",
		Description: "Use Claude Code's normal conservative approval behavior.",
	},
	{
		Value:       "auto",
		Label:       "Balanced",
		Description: "Let Claude handle routine work and ask when higher-risk actions need approval.",
	},
	{
		Value:       "plan",
		Label:       "Plan First",
		Description: "Have Claude plan before acting when the CLI supports it.",
	},
	{
		Value:       "acceptEdits",
		Label:       "Approve Edits",
		Description: "Auto-approve edits while still gating riskier actions when supported.",
	},
	{
		Value:       "dontAsk",
		Label:       "Don't Ask",
		Description: "Minimize approval prompts when the CLI supports it.",
	},
	{
		Value:       "skip",
		Label:       "Full Access",
		Description: "No permission prompts. Claude can edit files and run commands freely.",
	},
}

func ClaudeWizardOptionByValue(value string) (Option, int) {
	for i, option := range ClaudeWizardOptions {
		if option.Value == value {
			return option, i
		}
	}
	return ClaudeWizardOptions[0], 0
}
