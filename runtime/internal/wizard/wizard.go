// Package wizard provides an interactive setup wizard for the amurg runtime.
package wizard

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/amurg-ai/amurg/pkg/cli"
	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// Profile metadata for the chooser.
var profileDescriptions = map[string]string{
	protocol.ProfileClaudeCode:    "Claude Code (Anthropic CLI agent)",
	protocol.ProfileGitHubCopilot: "GitHub Copilot (gh copilot)",
	protocol.ProfileCodex:         "Codex (OpenAI CLI agent)",
	protocol.ProfileKilo:          "Kilo Code (open-source agent)",
	protocol.ProfileGenericCLI:    "Generic CLI (any interactive command)",
	protocol.ProfileGenericJob:    "Generic Job (run-to-completion command)",
	protocol.ProfileGenericHTTP:   "Generic HTTP (forward to URL)",
	protocol.ProfileExternal:      "External (JSON-Lines stdio protocol)",
}

// Display order for profiles — most common first.
var orderedProfiles = []string{
	protocol.ProfileClaudeCode,
	protocol.ProfileGitHubCopilot,
	protocol.ProfileCodex,
	protocol.ProfileKilo,
	protocol.ProfileGenericCLI,
	protocol.ProfileGenericJob,
	protocol.ProfileGenericHTTP,
	protocol.ProfileExternal,
}

// Wizard drives the interactive runtime config setup.
type Wizard struct {
	p *cli.Prompter
}

// New creates a Wizard using the given Prompter.
func New(p *cli.Prompter) *Wizard {
	return &Wizard{p: p}
}

// Run executes the interactive wizard and writes the config file.
func (w *Wizard) Run(outputPath string, generateSystemd bool) error {
	fmt.Fprintln(w.p.Out)
	fmt.Fprintln(w.p.Out, "  Amurg Runtime — Configuration Wizard")
	fmt.Fprintln(w.p.Out, strings.Repeat("─", 42))
	fmt.Fprintln(w.p.Out)

	cfg := &config.Config{}

	// Hub connection.
	fmt.Fprintln(w.p.Out, "Hub Connection")
	cfg.Hub.URL = w.p.Ask("  Hub WebSocket URL", "ws://localhost:8080/ws/runtime")
	cfg.Hub.Token = w.p.Ask("  Authentication token", "")
	fmt.Fprintln(w.p.Out)

	// Runtime settings.
	fmt.Fprintln(w.p.Out, "Runtime Settings")
	defaultID := "runtime-" + uuid.New().String()[:8]
	cfg.Runtime.ID = w.p.Ask("  Runtime ID", defaultID)
	cfg.Runtime.LogLevel = w.p.Ask("  Log level (debug/info/warn/error)", "info")
	fmt.Fprintln(w.p.Out)

	// Endpoints.
	fmt.Fprintln(w.p.Out, "Endpoints")
	numEndpoints := w.p.AskInt("  How many endpoints to configure?", 1)

	for i := range numEndpoints {
		fmt.Fprintf(w.p.Out, "\n  ── Endpoint %d of %d ──\n", i+1, numEndpoints)
		ep := w.configureEndpoint(i)
		cfg.Endpoints = append(cfg.Endpoints, ep)
	}

	// Output path.
	fmt.Fprintln(w.p.Out)
	if outputPath == "" {
		outputPath = w.p.Ask("Config file output path", "./amurg-runtime.json")
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(outputPath, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Fprintf(w.p.Out, "\n  Config written to %s\n", outputPath)

	// Optional systemd unit.
	if generateSystemd {
		if err := w.writeSystemdUnit(outputPath); err != nil {
			return err
		}
	}

	fmt.Fprintln(w.p.Out)
	fmt.Fprintln(w.p.Out, "  Next steps:")
	fmt.Fprintf(w.p.Out, "    amurg-runtime run %s\n\n", outputPath)

	return nil
}

func (w *Wizard) configureEndpoint(index int) config.EndpointConfig {
	// Build display options.
	options := make([]string, len(orderedProfiles))
	for i, p := range orderedProfiles {
		options[i] = fmt.Sprintf("%s — %s", p, profileDescriptions[p])
	}

	chosen := w.p.Choose("  Select profile", options, 0)

	// Extract profile name from "claude-code — Claude Code (...)".
	profileIdx := 0
	for i, opt := range options {
		if opt == chosen {
			profileIdx = i
			break
		}
	}
	profile := orderedProfiles[profileIdx]

	name := w.p.Ask("  Endpoint name", profileDescriptions[profile])
	epID := fmt.Sprintf("%s-%d", profile, index+1)

	ep := config.EndpointConfig{
		ID:      epID,
		Name:    name,
		Profile: profile,
	}

	switch profile {
	case protocol.ProfileClaudeCode:
		cc := &config.ClaudeCodeConfig{}
		model := w.p.Ask("  Model (leave empty for default)", "")
		if model != "" {
			cc.Model = model
		}
		perm := w.p.Ask("  Permission mode (leave empty for default)", "")
		if perm != "" {
			cc.PermissionMode = perm
		}
		ep.ClaudeCode = cc

	case protocol.ProfileGitHubCopilot:
		cp := &config.CopilotConfig{}
		model := w.p.Ask("  Model (leave empty for default)", "")
		if model != "" {
			cp.Model = model
		}
		ep.Copilot = cp

	case protocol.ProfileCodex:
		cx := &config.CodexConfig{}
		model := w.p.Ask("  Model (leave empty for default)", "")
		if model != "" {
			cx.Model = model
		}
		ep.Codex = cx

	case protocol.ProfileKilo:
		kc := &config.KiloConfig{}
		model := w.p.Ask("  Model (leave empty for default)", "")
		if model != "" {
			kc.Model = model
		}
		provider := w.p.Ask("  Provider (leave empty for default)", "")
		if provider != "" {
			kc.Provider = provider
		}
		ep.Kilo = kc

	case protocol.ProfileGenericCLI:
		command := w.p.Ask("  Command", "")
		argsStr := w.p.Ask("  Arguments (space-separated)", "")
		ep.CLI = &config.CLIConfig{
			Command: command,
			Args:    splitArgs(argsStr),
		}

	case protocol.ProfileGenericJob:
		command := w.p.Ask("  Command", "")
		argsStr := w.p.Ask("  Arguments (space-separated)", "")
		ep.Job = &config.JobConfig{
			Command: command,
			Args:    splitArgs(argsStr),
		}

	case protocol.ProfileGenericHTTP:
		baseURL := w.p.Ask("  Base URL", "")
		ep.HTTP = &config.HTTPConfig{
			BaseURL: baseURL,
		}

	case protocol.ProfileExternal:
		command := w.p.Ask("  Command", "")
		argsStr := w.p.Ask("  Arguments (space-separated)", "")
		ep.External = &config.ExternalConfig{
			Command: command,
			Args:    splitArgs(argsStr),
		}
	}

	return ep
}

func (w *Wizard) writeSystemdUnit(configPath string) error {
	unitPath := w.p.Ask("  Systemd unit file path", "/etc/systemd/system/amurg-runtime.service")

	// Resolve absolute config path for the unit file.
	absConfig := configPath
	if !strings.HasPrefix(configPath, "/") {
		wd, err := os.Getwd()
		if err == nil {
			absConfig = wd + "/" + configPath
		}
	}

	unit := fmt.Sprintf(`[Unit]
Description=Amurg Runtime
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/amurg-runtime run %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, absConfig)

	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	fmt.Fprintf(w.p.Out, "  Systemd unit written to %s\n", unitPath)
	fmt.Fprintln(w.p.Out, "  Enable with: sudo systemctl enable --now amurg-runtime")
	return nil
}

func splitArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}
