package protocol

// KnownProfiles defines the built-in profile names and their default capabilities.
var KnownProfiles = map[string]ProfileCaps{
	ProfileGenericCLI: {
		NativeSessionIDs: false,
		TurnCompletion:   false,
		ResumeAttach:     false,
		ExecModel:        ExecInteractive,
	},
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
	ProfileClaudeCode: {
		NativeSessionIDs: true,
		TurnCompletion:   true,
		ResumeAttach:     true,
		ExecModel:        ExecInteractive,
	},
	ProfileGitHubCopilot: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     true,
		ExecModel:        ExecInteractive,
	},
	ProfileCodex: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     true,
		ExecModel:        ExecRunToCompletion,
	},
	ProfileKilo: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     true,
		ExecModel:        ExecInteractive,
	},
	ProfileExternal: {
		NativeSessionIDs: false,
		TurnCompletion:   true,
		ResumeAttach:     false,
		ExecModel:        ExecInteractive,
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
	ProfileExternal      = "external"
)
