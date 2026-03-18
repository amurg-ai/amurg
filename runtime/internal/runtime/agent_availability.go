package runtime

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

func withAgentAvailability(agent config.AgentConfig, caps protocol.ProfileCaps) protocol.ProfileCaps {
	caps.Available = true
	caps.UnavailableReason = ""
	if reason := agentUnavailableReason(agent); reason != "" {
		caps.Available = false
		caps.UnavailableReason = reason
	}
	return caps
}

func agentUnavailableReason(agent config.AgentConfig) string {
	switch agent.Profile {
	case protocol.ProfileClaudeCode:
		cfg := agent.ClaudeCode
		cmd := "claude"
		workDir := ""
		if cfg != nil {
			if cfg.Command != "" {
				cmd = cfg.Command
			}
			workDir = cfg.WorkDir
		}
		if reason := missingCommandReason(cmd); reason != "" {
			return reason
		}
		return missingWorkDirReason(effectiveWorkDir(workDir, agent.Security))
	case protocol.ProfileGitHubCopilot:
		cfg := agent.Copilot
		cmd := "copilot"
		workDir := ""
		if cfg != nil {
			if cfg.Command != "" {
				cmd = cfg.Command
			}
			workDir = cfg.WorkDir
		}
		if reason := missingCommandReason(cmd); reason != "" {
			return reason
		}
		return missingWorkDirReason(effectiveWorkDir(workDir, agent.Security))
	case protocol.ProfileCodex:
		cfg := agent.Codex
		cmd := "codex"
		workDir := ""
		transport := ""
		if cfg != nil {
			if cfg.Command != "" {
				cmd = cfg.Command
			}
			workDir = cfg.WorkDir
			transport = cfg.Transport
		}
		if reason := missingCommandReason(cmd); reason != "" {
			return reason
		}
		if transport == "tmux" {
			if reason := missingCommandReason("tmux"); reason != "" {
				return fmt.Sprintf("codex transport requires %s", reason)
			}
		}
		return missingWorkDirReason(effectiveWorkDir(workDir, agent.Security))
	case protocol.ProfileKilo:
		cfg := agent.Kilo
		cmd := "kilo"
		workDir := ""
		if cfg != nil {
			if cfg.Command != "" {
				cmd = cfg.Command
			}
			workDir = cfg.WorkDir
		}
		if reason := missingCommandReason(cmd); reason != "" {
			return reason
		}
		return missingWorkDirReason(effectiveWorkDir(workDir, agent.Security))
	case protocol.ProfileGeminiCLI:
		cfg := agent.Gemini
		cmd := "gemini"
		workDir := ""
		if cfg != nil {
			if cfg.Command != "" {
				cmd = cfg.Command
			}
			workDir = cfg.WorkDir
		}
		if reason := missingCommandReason(cmd); reason != "" {
			return reason
		}
		return missingWorkDirReason(effectiveWorkDir(workDir, agent.Security))
	case protocol.ProfileGenericCLI:
		if agent.CLI == nil || agent.CLI.Command == "" {
			return "cli.command is not configured"
		}
		if reason := missingCommandReason(agent.CLI.Command); reason != "" {
			return reason
		}
		return missingWorkDirReason(effectiveWorkDir(agent.CLI.WorkDir, agent.Security))
	case protocol.ProfileGenericJob:
		if agent.Job == nil || agent.Job.Command == "" {
			return "job.command is not configured"
		}
		if reason := missingCommandReason(agent.Job.Command); reason != "" {
			return reason
		}
		return missingWorkDirReason(effectiveWorkDir(agent.Job.WorkDir, agent.Security))
	case protocol.ProfileExternal:
		if agent.External == nil || agent.External.Command == "" {
			return "external.command is not configured"
		}
		if reason := missingCommandReason(agent.External.Command); reason != "" {
			return reason
		}
		return missingWorkDirReason(effectiveWorkDir(agent.External.WorkDir, agent.Security))
	case protocol.ProfileGenericHTTP:
		if agent.HTTP == nil || agent.HTTP.BaseURL == "" {
			return "http.base_url is not configured"
		}
	}
	return ""
}

func effectiveWorkDir(workDir string, security *config.SecurityConfig) string {
	if security != nil && security.Cwd != "" {
		return security.Cwd
	}
	return workDir
}

func missingCommandReason(command string) string {
	if command == "" {
		return "command is not configured"
	}
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Sprintf("command %q is not on PATH", command)
	}
	return ""
}

func missingWorkDirReason(workDir string) string {
	if workDir == "" {
		return ""
	}
	info, err := os.Stat(workDir)
	if err == nil && info.IsDir() {
		return ""
	}
	return fmt.Sprintf("work_dir %q does not exist", workDir)
}
