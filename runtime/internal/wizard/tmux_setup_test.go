package wizard

import (
	"bytes"
	"fmt"
	"os/exec"
	"testing"

	"github.com/amurg-ai/amurg/pkg/cli"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

func TestConfigNeedsTMux(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{name: "nil config", cfg: nil, want: false},
		{name: "no agents", cfg: &config.Config{}, want: false},
		{name: "codex exec", cfg: &config.Config{Agents: []config.AgentConfig{{Codex: &config.CodexConfig{Transport: "exec"}}}}, want: false},
		{name: "codex tmux", cfg: &config.Config{Agents: []config.AgentConfig{{Codex: &config.CodexConfig{Transport: "tmux"}}}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConfigNeedsTMux(tt.cfg); got != tt.want {
				t.Fatalf("ConfigNeedsTMux() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnsureTMuxForConfig_UserDeclines(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	lookPath = func(name string) (string, error) {
		if name == "tmux" {
			return "", fmt.Errorf("missing")
		}
		return "/usr/bin/" + name, nil
	}

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: bytes.NewBufferString("n\n"), Out: out}
	cfg := &config.Config{Agents: []config.AgentConfig{{Codex: &config.CodexConfig{Transport: "tmux"}}}}
	if err := EnsureTMuxForConfig(p, cfg); err != nil {
		t.Fatalf("EnsureTMuxForConfig() error = %v", err)
	}
	if got := out.String(); !bytes.Contains([]byte(got), []byte("Install tmux manually")) {
		t.Fatalf("expected manual install guidance, got %q", got)
	}
}

func TestEnsureTMuxForConfig_InstallsTMux(t *testing.T) {
	origLookPath := lookPath
	origCommandRunner := commandRunner
	origGeteuid := geteuid
	t.Cleanup(func() {
		lookPath = origLookPath
		commandRunner = origCommandRunner
		geteuid = origGeteuid
	})

	installed := false
	lookPath = func(name string) (string, error) {
		switch name {
		case "tmux":
			if installed {
				return "/usr/bin/tmux", nil
			}
			return "", fmt.Errorf("missing")
		case "apt-get":
			return "/usr/bin/apt-get", nil
		default:
			return "", fmt.Errorf("missing")
		}
	}
	geteuid = func() int { return 0 }
	commandRunner = func(name string, args ...string) *exec.Cmd {
		installed = true
		return exec.Command("sh", "-c", "true")
	}

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: bytes.NewBufferString("y\n"), Out: out}
	cfg := &config.Config{Agents: []config.AgentConfig{{Codex: &config.CodexConfig{Transport: "tmux"}}}}
	if err := EnsureTMuxForConfig(p, cfg); err != nil {
		t.Fatalf("EnsureTMuxForConfig() error = %v", err)
	}
	if !installed {
		t.Fatal("expected install command to run")
	}
	if got := out.String(); !bytes.Contains([]byte(got), []byte("tmux installed successfully")) {
		t.Fatalf("expected success message, got %q", got)
	}
}
