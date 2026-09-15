package protocol

// KnownProfiles defines the built-in profile names and their default capabilities.
var KnownProfiles = map[string]ProfileCaps{
	ProfileGenericCLI: {Terminal: true, ResumeAttach: true, ExecModel: ExecInteractive},
	ProfileGenericJob: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     false,
		ExecModel:        ExecRunToCompletion,
	},
	ProfileGenericHTTP: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     false,
		ExecModel:        ExecRequestResponse,
	},
	ProfileClaudeCode:    {Terminal: true, ResumeAttach: true, ExecModel: ExecInteractive},
	ProfileGitHubCopilot: {Terminal: true, ResumeAttach: true, ExecModel: ExecInteractive},
	ProfileCodex:         {Terminal: true, ResumeAttach: true, ExecModel: ExecInteractive},
	ProfileKilo:          {Terminal: true, ResumeAttach: true, ExecModel: ExecInteractive},
	ProfileExternal: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     false,
		ExecModel:        ExecInteractive,
	},
	ProfileGeminiCLI: {Terminal: true, ResumeAttach: true, ExecModel: ExecInteractive},
}

// Profile name constants.
const (
	ProfileGenericCLI    = "generic-cli"
	ProfileGenericJob    = "generic-job"
	ProfileGenericHTTP   = "generic-http"
	ProfileClaudeCode    = "claude-code"
	ProfileGitHubCopilot = "github-copilot"
	ProfileCodex         = "codex"
	ProfileKilo          = "kilo-code"
	ProfileGeminiCLI     = "gemini-cli"
	ProfileExternal      = "external"
)
