package adapter

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

func TestInteractiveProfilesUsePersistentTerminalByDefault(t *testing.T) {
	commands := map[string]string{
		protocol.ProfileClaudeCode: "claude", protocol.ProfileCodex: "codex",
		protocol.ProfileGitHubCopilot: "copilot", protocol.ProfileGeminiCLI: "gemini",
		protocol.ProfileKilo: "kilo", protocol.ProfileGenericCLI: "bash",
	}
	for profile, command := range commands {
		t.Run(profile, func(t *testing.T) {
			cfg := config.AgentConfig{Profile: profile}
			if profile == protocol.ProfileGenericCLI {
				cfg.CLI = &config.CLIConfig{Command: command}
			}
			adp, err := DefaultRegistry().Get(profile)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := adp.(*TMuxAdapter); !ok {
				t.Fatalf("profile still uses %T", adp)
			}
			launch, err := terminalLaunchConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if launch.Command != command || len(launch.Args) != 0 || launch.SocketName != "amurg" {
				t.Fatalf("unexpected interactive launch: %+v", launch)
			}
			caps := protocol.KnownProfiles[profile]
			if !caps.Terminal || !caps.ResumeAttach || caps.TurnCompletion || caps.NativeSessionIDs || caps.ExecModel != protocol.ExecInteractive {
				t.Fatalf("wrong advertised capabilities: %+v", caps)
			}
		})
	}
	if _, err := DefaultRegistry().Get("tmux"); err == nil {
		t.Fatal("tmux must not be a separate user profile")
	}
}

func TestTerminalRetainsAgentSettingsWithoutPrintMode(t *testing.T) {
	dir := t.TempDir()
	for _, transport := range []string{"", "stream-json", "tmux"} {
		cfg := config.AgentConfig{Profile: protocol.ProfileClaudeCode, ClaudeCode: &config.ClaudeCodeConfig{
			Command: "custom-claude", WorkDir: dir, Env: map[string]string{"TEST": "value"},
			Model: "test-model", Transport: transport, PermissionMode: "plan", Args: []string{"--debug"},
		}}
		launch, err := terminalLaunchConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"--model", "test-model", "--permission-mode", "plan", "--debug"}
		if launch.Command != "custom-claude" || launch.WorkDir != dir || launch.Env["TEST"] != "value" || !reflect.DeepEqual(launch.Args, want) {
			t.Fatalf("legacy transport %q altered native launch: %+v", transport, launch)
		}
	}
	c, err := terminalLaunchConfig(config.AgentConfig{Profile: protocol.ProfileCodex, Codex: &config.CodexConfig{Model: "test-model", FullAuto: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Args, []string{"--model", "test-model", "--sandbox", "workspace-write", "--ask-for-approval", "on-request"}) {
		t.Fatalf("Codex should launch interactively with explicit settings: %v", c.Args)
	}
}

func TestOrdinaryAgentConfigReattachesSameProcess(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "agent")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nstty -echo\necho $$ > pid\nprintf 'READY\\n'\nwhile IFS= read -r line; do printf 'GOT:%s\\n' \"$line\"; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// Load the same shape the setup wizard writes: agent + directory, no tmux block.
	raw, _ := json.Marshal(map[string]any{"hub": map[string]string{"url": "ws://localhost", "token": "test"}, "runtime": map[string]string{"id": "test"}, "agents": []any{map[string]any{
		"id": "claude", "profile": "claude-code", "name": "Test agent", "claude_code": map[string]any{"command": fake, "work_dir": dir, "transport": "stream-json"},
	}}})
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := loaded.Agents[0]
	if cfg.TMux != nil {
		t.Fatal("user configuration should not need tmux settings")
	}
	cfg.SessionKey = dir
	adp, err := DefaultRegistry().Get(cfg.Profile)
	if err != nil {
		t.Fatal(err)
	}
	start := func() *terminalSession {
		a, err := adp.Start(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		s := a.(*terminalSession)
		t.Cleanup(func() { _ = s.Close() })
		if err := s.Attach(context.Background(), 80, 24); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := start()
	// Remove only our unique test session on the managed server.
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", "amurg", "kill-session", "-t", "="+s.name).Run() })
	waitTerminalText(t, s, "READY")
	pid, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Input(context.Background(), s.client.generation, []byte("first\r")); err != nil {
		t.Fatal(err)
	}
	waitTerminalText(t, s, "GOT:first")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := start()
	waitTerminalText(t, s2, "GOT:first")
	after, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil || strings.TrimSpace(string(pid)) != strings.TrimSpace(string(after)) {
		t.Fatalf("process was restarted: %q -> %q (%v)", pid, after, err)
	}
	if err := s2.Input(context.Background(), s2.client.generation, []byte("second\r")); err != nil {
		t.Fatal(err)
	}
	waitTerminalText(t, s2, "GOT:second")
}
