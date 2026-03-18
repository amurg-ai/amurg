package wizard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amurg-ai/amurg/pkg/cli"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

func TestWizard_ClaudeCode_ManualToken(t *testing.T) {
	// Simulate user input: self-hosted hub, manual token, configure one claude-code agent.
	input := strings.Join([]string{
		"2",                      // hub: Self-hosted
		"http://hub.example.com", // hub URL
		"2",                      // auth: Enter token manually
		"my-token-123",           // token
		"test-runtime",           // runtime ID
		"info",                   // log level
		"1",                      // 1 agent
		"1",                      // profile: claude-code (first option)
		"My Claude Agent",        // agent name
		"",                       // model (default)
		"",                       // permission mode (default)
		"2",                      // start now: No
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "config.json")

	w := New(p)
	if _, _, err := w.Run(outputPath, false); err != nil {
		t.Fatalf("wizard.Run() error: %v", err)
	}

	// Read and validate the generated config.
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if cfg.Hub.URL != "ws://hub.example.com/ws/runtime" {
		t.Errorf("hub.url = %q, want %q", cfg.Hub.URL, "ws://hub.example.com/ws/runtime")
	}
	if cfg.Hub.Token != "my-token-123" {
		t.Errorf("hub.token = %q, want %q", cfg.Hub.Token, "my-token-123")
	}
	if cfg.Runtime.ID != "test-runtime" {
		t.Errorf("runtime.id = %q, want %q", cfg.Runtime.ID, "test-runtime")
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("agents count = %d, want 1", len(cfg.Agents))
	}
	agent := cfg.Agents[0]
	if agent.Profile != "claude-code" {
		t.Errorf("agent profile = %q, want %q", agent.Profile, "claude-code")
	}
	if agent.Name != "My Claude Agent" {
		t.Errorf("agent name = %q, want %q", agent.Name, "My Claude Agent")
	}
}

func TestWizard_CloudHub(t *testing.T) {
	// Simulate: choose Amurg Cloud, manual token.
	input := strings.Join([]string{
		"1",               // hub: Amurg Cloud
		"2",               // auth: Enter token manually
		"cloud-token-xyz", // token
		"cloud-runtime",   // runtime ID
		"info",            // log level
		"1",               // 1 agent
		"1",               // profile: claude-code
		"Cloud Agent",     // agent name
		"",                // model (default)
		"",                // permission mode (default)
		"2",               // start now: No
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "config.json")

	w := New(p)
	if _, _, err := w.Run(outputPath, false); err != nil {
		t.Fatalf("wizard.Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if cfg.Hub.URL != "wss://hub.amurg.ai/ws/runtime" {
		t.Errorf("hub.url = %q, want %q", cfg.Hub.URL, "wss://hub.amurg.ai/ws/runtime")
	}
	if cfg.Hub.Token != "cloud-token-xyz" {
		t.Errorf("hub.token = %q, want %q", cfg.Hub.Token, "cloud-token-xyz")
	}
}

func TestWizard_GenericCLI(t *testing.T) {
	input := strings.Join([]string{
		"2",                     // hub: Self-hosted
		"http://localhost:8080", // hub URL
		"2",                     // auth: Enter token manually
		"dev-token",             // token
		"dev-runtime",           // runtime ID
		"debug",                 // log level
		"1",                     // 1 agent
		"5",                     // profile: generic-cli (5th option)
		"Bash Shell",            // agent name
		"bash",                  // command
		"--norc -i",             // args
		"2",                     // start now: No
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "config.json")

	w := New(p)
	if _, _, err := w.Run(outputPath, false); err != nil {
		t.Fatalf("wizard.Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if len(cfg.Agents) != 1 {
		t.Fatalf("agents count = %d, want 1", len(cfg.Agents))
	}
	agent := cfg.Agents[0]
	if agent.Profile != "generic-cli" {
		t.Errorf("agent profile = %q, want %q", agent.Profile, "generic-cli")
	}
	if agent.CLI == nil {
		t.Fatal("agent.cli is nil")
	}
	if agent.CLI.Command != "bash" {
		t.Errorf("cli.command = %q, want %q", agent.CLI.Command, "bash")
	}
	if len(agent.CLI.Args) != 2 || agent.CLI.Args[0] != "--norc" {
		t.Errorf("cli.args = %v, want [--norc -i]", agent.CLI.Args)
	}
}

func TestWizard_CodexTMux_DeclinedInstall(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	lookPath = func(name string) (string, error) {
		if name == "tmux" {
			return "", os.ErrNotExist
		}
		return "/usr/bin/" + name, nil
	}

	input := strings.Join([]string{
		"2",
		"http://localhost:8080",
		"2",
		"dev-token",
		"codex-runtime",
		"info",
		"1",
		"3",
		"Codex Agent",
		"",
		"2",
		"n",
		"2",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "config.json")

	w := New(p)
	if _, _, err := w.Run(outputPath, false); err != nil {
		t.Fatalf("wizard.Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Codex == nil {
		t.Fatalf("expected one codex agent, got %#v", cfg.Agents)
	}
	if cfg.Agents[0].Codex.Transport != "tmux" {
		t.Fatalf("codex transport = %q, want tmux", cfg.Agents[0].Codex.Transport)
	}
	if !strings.Contains(out.String(), "Install tmux manually") {
		t.Fatalf("expected tmux guidance in output, got %q", out.String())
	}
}

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"  ", 0},
		{"--norc -i", 2},
		{"one", 1},
	}
	for _, tt := range tests {
		got := splitArgs(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitArgs(%q) returned %d args, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestWizard_SelfHostedBareHostNormalizesToRuntimeWSURL(t *testing.T) {
	input := strings.Join([]string{
		"2",
		"localhost:8090",
		"2",
		"token-123",
		"runtime-local",
		"info",
		"1",
		"1",
		"Claude",
		"",
		"",
		"2",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "config.json")

	w := New(p)
	if _, _, err := w.Run(outputPath, false); err != nil {
		t.Fatalf("wizard.Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if cfg.Hub.URL != "ws://localhost:8090/ws/runtime" {
		t.Fatalf("hub.url = %q, want %q", cfg.Hub.URL, "ws://localhost:8090/ws/runtime")
	}
}
