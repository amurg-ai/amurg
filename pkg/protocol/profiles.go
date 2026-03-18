package protocol

// KnownProfiles defines the built-in profile names and their default capabilities.
var KnownProfiles = map[string]ProfileCaps{
	ProfileGenericCLI: {
		NativeSessionIDs: false,
		TurnCompletion:   false,
		ResumeAttach:     false,
		ExecModel:        ExecInteractive,
		Available:        true,
	},
	ProfileGenericJob: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     false,
		ExecModel:        ExecRunToCompletion,
		Available:        true,
	},
	ProfileGenericHTTP: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     false,
		ExecModel:        ExecRequestResponse,
		Available:        true,
	},
	ProfileClaudeCode: {
		NativeSessionIDs: true,
		TurnCompletion:   true,
		ResumeAttach:     true,
		ExecModel:        ExecInteractive,
		Available:        true,
	},
	ProfileGitHubCopilot: {
		NativeSessionIDs: true,
		TurnCompletion:   true,
		ResumeAttach:     true,
		ExecModel:        ExecInteractive,
		Available:        true,
	},
	ProfileCodex: {
		NativeSessionIDs: true,
		TurnCompletion:   true,
		ResumeAttach:     true,
		ExecModel:        ExecRunToCompletion,
		Available:        true,
	},
	ProfileKilo: {
		NativeSessionIDs: true,
		TurnCompletion:   true,
		ResumeAttach:     true,
		ExecModel:        ExecInteractive,
		Available:        true,
	},
	ProfileExternal: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     false,
		ExecModel:        ExecInteractive,
		Available:        true,
	},
	ProfileGeminiCLI: {
		NativeSessionIDs: true,
		TurnCompletion:   true,
		ResumeAttach:     true,
		ExecModel:        ExecInteractive,
		Available:        true,
	},
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
