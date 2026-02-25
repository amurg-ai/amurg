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
	// Simulate user input: self-hosted hub, manual token, configure one claude-code endpoint.
	input := strings.Join([]string{
		"2",                               // hub: Self-hosted
		"ws://hub.example.com/ws/runtime", // hub URL
		"2",                               // auth: Enter token manually
		"my-token-123",                    // token
		"test-runtime",                    // runtime ID
		"info",                            // log level
		"1",                               // 1 endpoint
		"1",                               // profile: claude-code (first option)
		"My Claude Agent",                 // endpoint name
		"",                                // model (default)
		"",                                // permission mode (default)
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "config.json")

	w := New(p)
	if err := w.Run(outputPath, false); err != nil {
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
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("endpoints count = %d, want 1", len(cfg.Endpoints))
	}
	ep := cfg.Endpoints[0]
	if ep.Profile != "claude-code" {
		t.Errorf("endpoint profile = %q, want %q", ep.Profile, "claude-code")
	}
	if ep.Name != "My Claude Agent" {
		t.Errorf("endpoint name = %q, want %q", ep.Name, "My Claude Agent")
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
		"1",               // 1 endpoint
		"1",               // profile: claude-code
		"Cloud Agent",     // endpoint name
		"",                // model (default)
		"",                // permission mode (default)
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "config.json")

	w := New(p)
	if err := w.Run(outputPath, false); err != nil {
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
		"2",                                 // hub: Self-hosted
		"ws://localhost:8080/ws/runtime",     // hub URL
		"2",                                 // auth: Enter token manually
		"dev-token",                         // token
		"dev-runtime",                       // runtime ID
		"debug",                             // log level
		"1",                                 // 1 endpoint
		"5",                                 // profile: generic-cli (5th option)
		"Bash Shell",                        // endpoint name
		"bash",                              // command
		"--norc -i",                         // args
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "config.json")

	w := New(p)
	if err := w.Run(outputPath, false); err != nil {
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

	if len(cfg.Endpoints) != 1 {
		t.Fatalf("endpoints count = %d, want 1", len(cfg.Endpoints))
	}
	ep := cfg.Endpoints[0]
	if ep.Profile != "generic-cli" {
		t.Errorf("endpoint profile = %q, want %q", ep.Profile, "generic-cli")
	}
	if ep.CLI == nil {
		t.Fatal("endpoint.cli is nil")
	}
	if ep.CLI.Command != "bash" {
		t.Errorf("cli.command = %q, want %q", ep.CLI.Command, "bash")
	}
	if len(ep.CLI.Args) != 2 || ep.CLI.Args[0] != "--norc" {
		t.Errorf("cli.args = %v, want [--norc -i]", ep.CLI.Args)
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

func TestWsToHTTP(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"wss://hub.amurg.ai/ws/runtime", "https://hub.amurg.ai"},
		{"ws://localhost:8080/ws/runtime", "http://localhost:8080"},
		{"wss://example.com:443/ws/runtime", "https://example.com:443"},
		{"ws://10.0.0.1:3000/ws/runtime", "http://10.0.0.1:3000"},
	}
	for _, tt := range tests {
		got := wsToHTTP(tt.input)
		if got != tt.want {
			t.Errorf("wsToHTTP(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
