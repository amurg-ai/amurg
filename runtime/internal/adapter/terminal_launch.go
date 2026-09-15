package adapter

import (
	"fmt"
	"maps"
	"strings"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// terminalLaunchConfig translates ordinary agent settings into an interactive
// process. Transport and session naming are runtime implementation details.
func terminalLaunchConfig(cfg config.AgentConfig) (config.TMuxConfig, error) {
	t := config.TMuxConfig{SocketName: "amurg", WorkDir: resolveWorkDir(cfg.WorkDir(), cfg.Security)}
	var extra []string
	option := func(flag, value string) {
		if value != "" {
			t.Args = append(t.Args, flag, value)
		}
	}
	tools := func(flag string, values []string) {
		for _, value := range values {
			option(flag, value)
		}
	}
	permission := func(value string) string {
		if cfg.Security != nil && cfg.Security.PermissionMode != "" {
			return cfg.Security.PermissionMode
		}
		return value
	}
	switch cfg.Profile {
	case protocol.ProfileClaudeCode:
		c := config.ClaudeCodeConfig{}
		if cfg.ClaudeCode != nil {
			c = *cfg.ClaudeCode
		}
		t.Command, t.Env, extra = c.Command, c.Env, c.Args
		if t.Command == "" {
			t.Command = "claude"
		}
		option("--model", c.Model)
		switch p := permission(c.PermissionMode); p {
		case "skip", "bypassPermissions", "dangerously-skip-permissions":
			t.Args = append(t.Args, "--dangerously-skip-permissions")
		case "acceptEdits", "plan":
			option("--permission-mode", p)
		}
		allowed, denied := c.AllowedTools, c.DisallowedTools
		if cfg.Security != nil {
			if cfg.Security.AllowedTools != nil {
				allowed = cfg.Security.AllowedTools
			}
			if cfg.Security.DisallowedTools != nil {
				denied = cfg.Security.DisallowedTools
			}
		}
		tools("--allowedTools", allowed)
		tools("--disallowedTools", denied)
		option("--system-prompt", c.SystemPrompt)
	case protocol.ProfileCodex:
		c := config.CodexConfig{}
		if cfg.Codex != nil {
			c = *cfg.Codex
		}
		t.Command, t.Env, extra = c.Command, c.Env, c.Args
		if t.Command == "" {
			t.Command = "codex"
		}
		option("--model", c.Model)
		option("--profile", c.Profile)
		if c.FullAuto {
			if c.SandboxMode == "" {
				c.SandboxMode = "workspace-write"
			}
			if c.ApprovalMode == "" {
				c.ApprovalMode = "on-request"
			}
		}
		option("--sandbox", c.SandboxMode)
		option("--ask-for-approval", c.ApprovalMode)
		if p := permission(""); p == "skip" || p == "bypassPermissions" {
			t.Args = append(t.Args, "--dangerously-bypass-approvals-and-sandbox")
		}
		tools("--add-dir", c.AdditionalDirs)
	case protocol.ProfileGitHubCopilot:
		c := config.CopilotConfig{}
		if cfg.Copilot != nil {
			c = *cfg.Copilot
		}
		t.Command, t.Env, extra = c.Command, c.Env, c.Args
		if t.Command == "" {
			t.Command = "copilot"
		}
		option("--model", c.Model)
		if p := permission(""); p == "skip" || p == "bypassPermissions" {
			t.Args = append(t.Args, "--allow-all")
		}
		allowed, denied := c.AllowedTools, c.DeniedTools
		if cfg.Security != nil {
			if cfg.Security.AllowedTools != nil {
				allowed = cfg.Security.AllowedTools
			}
			denied = append(append([]string{}, denied...), cfg.Security.DisallowedTools...)
		}
		tools("--allow-tool", allowed)
		tools("--deny-tool", denied)
	case protocol.ProfileKilo:
		c := config.KiloConfig{}
		if cfg.Kilo != nil {
			c = *cfg.Kilo
		}
		t.Command, t.Env, extra = c.Command, c.Env, c.Args
		if t.Command == "" {
			t.Command = "kilo"
		}
		model := c.Model
		if model != "" && c.Provider != "" && !strings.Contains(model, "/") {
			model = c.Provider + "/" + model
		}
		option("--model", model)
		option("--agent", c.Mode)
	case protocol.ProfileGeminiCLI:
		c := config.GeminiCLIConfig{}
		if cfg.Gemini != nil {
			c = *cfg.Gemini
		}
		t.Command, t.Env, extra = c.Command, maps.Clone(c.Env), c.Args
		if t.Command == "" {
			t.Command = "gemini"
		}
		option("--model", c.Model)
		p := permission(c.ApprovalMode)
		switch p {
		case "skip", "bypassPermissions", "yolo":
			t.Args = append(t.Args, "--yolo")
		case "default", "auto_edit":
			option("--approval-mode", p)
		}
		if c.Sandbox {
			t.Args = append(t.Args, "--sandbox")
		}
		option("--include-directories", strings.Join(c.IncludeDirs, ","))
		if c.SystemPromptFile != "" {
			if t.Env == nil {
				t.Env = make(map[string]string)
			}
			t.Env["GEMINI_SYSTEM_MD"] = c.SystemPromptFile
		}
	case protocol.ProfileGenericCLI:
		if cfg.CLI == nil || cfg.CLI.Command == "" {
			return t, fmt.Errorf("interactive agent requires a command")
		}
		t.Command, t.Env, extra = cfg.CLI.Command, cfg.CLI.Env, cfg.CLI.Args
	default:
		return t, fmt.Errorf("profile %q is not an interactive agent", cfg.Profile)
	}
	t.Args = append(t.Args, extra...)
	return t, nil
}
