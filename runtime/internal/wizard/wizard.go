// Package wizard provides an interactive setup wizard for the amurg runtime.
package wizard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

// deviceCodeResponse is the JSON body returned by POST /api/runtime/register.
type deviceCodeResponse struct {
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	PollingToken    string `json:"polling_token"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// pollResponse is the JSON body returned by POST /api/runtime/register/poll.
type pollResponse struct {
	Status    string `json:"status"` // "pending", "approved", "expired"
	Token     string `json:"token,omitempty"`
	RuntimeID string `json:"runtime_id,omitempty"`
	OrgID     string `json:"org_id,omitempty"`
}

// Run executes the interactive wizard and writes the config file.
// It returns the resolved config path, whether the user wants to start the
// runtime immediately, and any error.
func (w *Wizard) Run(outputPath string, generateSystemd bool) (string, bool, error) {
	_, _ = fmt.Fprintln(w.p.Out)
	_, _ = fmt.Fprintln(w.p.Out, "  Amurg Runtime — Configuration Wizard")
	_, _ = fmt.Fprintln(w.p.Out, strings.Repeat("─", 42))
	_, _ = fmt.Fprintln(w.p.Out)

	cfg := &config.Config{}

	// Hub connection.
	_, _ = fmt.Fprintln(w.p.Out, "Hub Connection")
	hubChoice := w.p.Choose("  Where is your Amurg Hub?",
		[]string{"Amurg Cloud (hub.amurg.ai)", "Self-hosted"}, 0)

	if hubChoice == "Amurg Cloud (hub.amurg.ai)" {
		cfg.Hub.URL = "wss://hub.amurg.ai/ws/runtime"
	} else {
		cfg.Hub.URL = w.p.Ask("  Hub WebSocket URL", "ws://localhost:8080/ws/runtime")
	}
	_, _ = fmt.Fprintln(w.p.Out)

	// Authentication.
	_, _ = fmt.Fprintln(w.p.Out, "Authentication")
	authChoice := w.p.Choose("  How would you like to authenticate?",
		[]string{"Register via browser (recommended)", "Enter token manually"}, 0)

	runtimeIDFromAuth := ""
	if authChoice == "Register via browser (recommended)" {
		token, runtimeID, orgID, err := w.deviceCodeFlow(cfg.Hub.URL)
		if err != nil {
			return "", false, err
		}
		cfg.Hub.Token = token
		runtimeIDFromAuth = runtimeID
		cfg.Runtime.OrgID = orgID
	} else {
		cfg.Hub.Token = w.p.Ask("  Authentication token", "")
	}
	_, _ = fmt.Fprintln(w.p.Out)

	// Runtime settings.
	_, _ = fmt.Fprintln(w.p.Out, "Runtime Settings")
	if runtimeIDFromAuth != "" {
		cfg.Runtime.ID = runtimeIDFromAuth
		_, _ = fmt.Fprintf(w.p.Out, "  Runtime ID: %s (from registration)\n", runtimeIDFromAuth)
	} else {
		defaultID := "runtime-" + uuid.New().String()[:8]
		cfg.Runtime.ID = w.p.Ask("  Runtime ID", defaultID)
	}
	cfg.Runtime.LogLevel = w.p.Ask("  Log level (debug/info/warn/error)", "info")
	_, _ = fmt.Fprintln(w.p.Out)

	// Agents.
	_, _ = fmt.Fprintln(w.p.Out, "Agents")
	numAgents := w.p.AskInt("  How many agents to configure?", 1)

	for i := range numAgents {
		_, _ = fmt.Fprintf(w.p.Out, "\n  ── Agent %d of %d ──\n", i+1, numAgents)
		agent := w.ConfigureAgent(i)
		cfg.Agents = append(cfg.Agents, agent)
	}

	// Output path — use default unless explicitly overridden via flag.
	if outputPath == "" {
		outputPath = DefaultConfigPath()
	}

	// Ensure parent directory exists.
	if dir := filepath.Dir(outputPath); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", false, fmt.Errorf("create config directory: %w", err)
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", false, fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(outputPath, append(data, '\n'), 0600); err != nil {
		return "", false, fmt.Errorf("write config: %w", err)
	}

	_, _ = fmt.Fprintf(w.p.Out, "\n  Config written to %s\n", outputPath)

	// Optional systemd unit.
	if generateSystemd {
		if err := w.writeSystemdUnit(outputPath); err != nil {
			return "", false, err
		}
	}

	// Offer to start the runtime.
	_, _ = fmt.Fprintln(w.p.Out)
	startChoice := w.p.Choose("  Start the runtime now?",
		[]string{"Yes", "No"}, 0)
	startNow := startChoice == "Yes"

	if !startNow {
		_, _ = fmt.Fprintln(w.p.Out)
		_, _ = fmt.Fprintln(w.p.Out, "  To start later:")
		_, _ = fmt.Fprintf(w.p.Out, "    amurg-runtime run %s\n\n", outputPath)
	}

	return outputPath, startNow, nil
}

// deviceCodeFlow initiates the device-code registration flow and polls until
// approval or expiry. On expiry it offers retry or fallback to manual token.
// Returns (token, runtimeID, orgID, error).
func (w *Wizard) deviceCodeFlow(hubWSURL string) (string, string, string, error) {
	httpBase := wsToHTTP(hubWSURL)

	for {
		token, runtimeID, orgID, err := w.doDeviceCodeRound(httpBase)
		if err != nil {
			return "", "", "", err
		}
		if token != "" {
			return token, runtimeID, orgID, nil
		}

		// Code expired — offer retry or fallback.
		_, _ = fmt.Fprintln(w.p.Out, "  Registration code expired.")
		retryChoice := w.p.Choose("  What would you like to do?",
			[]string{"Try again", "Enter token manually"}, 0)
		if retryChoice == "Enter token manually" {
			manualToken := w.p.Ask("  Authentication token", "")
			return manualToken, "", "", nil
		}
		// Loop to retry.
	}
}

// doDeviceCodeRound performs one round of the device-code flow: request a code,
// display it, then poll. Returns the token on success, or empty strings if expired.
func (w *Wizard) doDeviceCodeRound(httpBase string) (string, string, string, error) {
	// Request a device code.
	resp, err := http.Post(httpBase+"/api/runtime/register", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		return "", "", "", fmt.Errorf("request device code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("device code request failed with status %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		return "", "", "", fmt.Errorf("hub at %s does not support device-code registration (got %s response). Make sure your hub is up to date", httpBase, ct)
	}

	var dcResp deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dcResp); err != nil {
		return "", "", "", fmt.Errorf("decode device code response: %w", err)
	}

	// Display the code in a box.
	_, _ = fmt.Fprintln(w.p.Out)
	_, _ = fmt.Fprintln(w.p.Out, "  ┌─────────────────────────────────────────┐")
	_, _ = fmt.Fprintf(w.p.Out, "  │  Your code:  %-26s │\n", dcResp.UserCode)
	_, _ = fmt.Fprintf(w.p.Out, "  │  Open:  %-31s │\n", dcResp.VerificationURL)
	_, _ = fmt.Fprintln(w.p.Out, "  └─────────────────────────────────────────┘")
	_, _ = fmt.Fprintln(w.p.Out)

	// Try to open the browser automatically.
	if err := openBrowser(dcResp.VerificationURL); err != nil {
		_, _ = fmt.Fprintln(w.p.Out, "  Could not open browser automatically. Please open the URL above.")
	} else {
		_, _ = fmt.Fprintln(w.p.Out, "  Browser opened — approve the registration there.")
	}
	_, _ = fmt.Fprintln(w.p.Out, "  Waiting for approval...")

	// Poll until approved or expired.
	interval := time.Duration(dcResp.Interval) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dcResp.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(interval)

		pollBody, _ := json.Marshal(map[string]string{"polling_token": dcResp.PollingToken})
		pollResp, err := http.Post(httpBase+"/api/runtime/register/poll", "application/json", bytes.NewBuffer(pollBody))
		if err != nil {
			_, _ = fmt.Fprintf(w.p.Out, "  Poll error: %v (retrying...)\n", err)
			continue
		}

		var pr pollResponse
		decodeErr := json.NewDecoder(pollResp.Body).Decode(&pr)
		_ = pollResp.Body.Close()
		if decodeErr != nil {
			_, _ = fmt.Fprintf(w.p.Out, "  Poll decode error: %v (retrying...)\n", decodeErr)
			continue
		}

		switch pr.Status {
		case "approved":
			_, _ = fmt.Fprintln(w.p.Out, "  Registration approved!")
			return pr.Token, pr.RuntimeID, pr.OrgID, nil
		case "expired":
			return "", "", "", nil
		}
		// "pending" — keep polling.
	}

	// Timed out locally.
	return "", "", "", nil
}

// wsToHTTP converts a WebSocket URL to its HTTP equivalent.
// wss://host/ws/runtime → https://host
// ws://host/ws/runtime  → http://host
func wsToHTTP(wsURL string) string {
	u := wsURL
	if strings.HasPrefix(u, "wss://") {
		u = "https://" + strings.TrimPrefix(u, "wss://")
	} else if strings.HasPrefix(u, "ws://") {
		u = "http://" + strings.TrimPrefix(u, "ws://")
	}
	u = strings.TrimSuffix(u, "/ws/runtime")
	return u
}

// ConfigureAgent runs the interactive prompt to configure a single agent.
func (w *Wizard) ConfigureAgent(index int) config.AgentConfig {
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

	name := w.p.Ask("  Agent name", profileDescriptions[profile])
	agentID := fmt.Sprintf("%s-%d", profile, index+1)

	agent := config.AgentConfig{
		ID:      agentID,
		Name:    name,
		Profile: profile,
	}

	// Default working directory for agents that support it.
	defaultWorkDir, _ := os.Getwd()
	if defaultWorkDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			defaultWorkDir = home
		}
	}

	switch profile {
	case protocol.ProfileClaudeCode:
		cc := &config.ClaudeCodeConfig{}
		cc.WorkDir = w.p.Ask("  Working directory", defaultWorkDir)
		model := w.p.Ask("  Model (leave empty for default)", "")
		if model != "" {
			cc.Model = model
		}
		perm := w.p.Ask("  Permission mode (leave empty for default)", "")
		if perm != "" {
			cc.PermissionMode = perm
		}
		agent.ClaudeCode = cc

	case protocol.ProfileGitHubCopilot:
		cp := &config.CopilotConfig{}
		cp.WorkDir = w.p.Ask("  Working directory", defaultWorkDir)
		model := w.p.Ask("  Model (leave empty for default)", "")
		if model != "" {
			cp.Model = model
		}
		agent.Copilot = cp

	case protocol.ProfileCodex:
		cx := &config.CodexConfig{}
		cx.WorkDir = w.p.Ask("  Working directory", defaultWorkDir)
		model := w.p.Ask("  Model (leave empty for default)", "")
		if model != "" {
			cx.Model = model
		}
		agent.Codex = cx

	case protocol.ProfileKilo:
		kc := &config.KiloConfig{}
		kc.WorkDir = w.p.Ask("  Working directory", defaultWorkDir)
		model := w.p.Ask("  Model (leave empty for default)", "")
		if model != "" {
			kc.Model = model
		}
		provider := w.p.Ask("  Provider (leave empty for default)", "")
		if provider != "" {
			kc.Provider = provider
		}
		agent.Kilo = kc

	case protocol.ProfileGenericCLI:
		command := w.p.Ask("  Command", "")
		argsStr := w.p.Ask("  Arguments (space-separated)", "")
		agent.CLI = &config.CLIConfig{
			Command: command,
			Args:    splitArgs(argsStr),
		}

	case protocol.ProfileGenericJob:
		command := w.p.Ask("  Command", "")
		argsStr := w.p.Ask("  Arguments (space-separated)", "")
		agent.Job = &config.JobConfig{
			Command: command,
			Args:    splitArgs(argsStr),
		}

	case protocol.ProfileGenericHTTP:
		baseURL := w.p.Ask("  Base URL", "")
		agent.HTTP = &config.HTTPConfig{
			BaseURL: baseURL,
		}

	case protocol.ProfileExternal:
		command := w.p.Ask("  Command", "")
		argsStr := w.p.Ask("  Arguments (space-separated)", "")
		agent.External = &config.ExternalConfig{
			Command: command,
			Args:    splitArgs(argsStr),
		}
	}

	return agent
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

	_, _ = fmt.Fprintf(w.p.Out, "  Systemd unit written to %s\n", unitPath)
	_, _ = fmt.Fprintln(w.p.Out, "  Enable with: sudo systemctl enable --now amurg-runtime")
	return nil
}

// openBrowser attempts to open a URL in the default browser.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default: // linux, freebsd, etc.
		return exec.Command("xdg-open", url).Start()
	}
}

// DefaultConfigPath returns the default config file path (~/.amurg/config.json).
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "amurg-runtime.json"
	}
	return filepath.Join(home, ".amurg", "config.json")
}

func splitArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}
