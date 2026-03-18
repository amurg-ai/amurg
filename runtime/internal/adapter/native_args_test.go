package adapter

import (
	"encoding/json"
	"testing"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

func TestBuildClaudeArgs_UsesSecurityOverridesAndAllowedPaths(t *testing.T) {
	cfg := config.ClaudeCodeConfig{
		Model:           "sonnet",
		MaxTurns:        2,
		AllowedTools:    []string{"Read"},
		DisallowedTools: []string{"Write", "Bash"},
		SystemPrompt:    "You are helpful.",
	}
	security := &config.SecurityConfig{
		PermissionMode:  "auto",
		AllowedPaths:    []string{"/tmp/work", "/tmp/extra"},
		AllowedTools:    []string{"Edit"},
		DisallowedTools: []string{"Write", "Bash"},
	}

	args, skipPerms := buildClaudeArgs(cfg, security, "native-1", []string{"Write"})

	if skipPerms {
		t.Fatal("expected auto mode, not skip-permissions")
	}
	assertHasArgPair(t, args, "--permission-mode", "auto")
	assertHasArgPair(t, args, "--model", "sonnet")
	assertHasArgPair(t, args, "--max-turns", "2")
	assertHasArgPair(t, args, "--allowedTools", "Edit")
	assertHasArgPair(t, args, "--allowedTools", "Write")
	assertHasArgPair(t, args, "--add-dir", "/tmp/work")
	assertHasArgPair(t, args, "--add-dir", "/tmp/extra")
	assertHasArgPair(t, args, "--resume", "native-1")
	if hasArgPair(args, "--disallowedTools", "Write") {
		t.Fatal("expected temporary allowed tool to be removed from disallowed list")
	}
}

func TestBuildClaudeArgs_SkipPermissionsUsesDangerousFlag(t *testing.T) {
	args, skipPerms := buildClaudeArgs(config.ClaudeCodeConfig{PermissionMode: "skip"}, nil, "", nil)

	if !skipPerms {
		t.Fatal("expected skip-permissions mode to set skipPerms")
	}
	assertHasArg(t, args, "--dangerously-skip-permissions")
}

func TestParseClaudePermissionDenials(t *testing.T) {
	raw, err := json.Marshal([]map[string]string{
		{"tool": "Write", "description": "Need write access", "resource": "/tmp/proof.txt"},
		{"name": "Bash", "path": "/tmp"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	denials := parseClaudePermissionDenials(raw)
	if len(denials) != 2 {
		t.Fatalf("expected 2 denials, got %d", len(denials))
	}
	if denials[0].Tool != "Write" || denials[0].Resource != "/tmp/proof.txt" {
		t.Fatalf("unexpected first denial: %+v", denials[0])
	}
	if denials[1].Tool != "Bash" {
		t.Fatalf("unexpected second denial tool: %+v", denials[1])
	}
	if denials[1].Description == "" {
		t.Fatalf("expected fallback description for second denial: %+v", denials[1])
	}
}

func TestBuildCodexArgs_UsesRootApprovalFlagsAndAllowedPaths(t *testing.T) {
	workDir := t.TempDir()
	extraDir := t.TempDir()
	allowedDir := t.TempDir()

	cfg := config.CodexConfig{
		Model:          "gpt-5.3-codex",
		SandboxMode:    "workspace-write",
		Profile:        "team",
		WorkDir:        workDir,
		AdditionalDirs: []string{extraDir},
	}
	security := &config.SecurityConfig{
		PermissionMode: "auto",
		AllowedPaths:   []string{allowedDir},
	}

	args := buildCodexArgs(cfg, security, "thread-1", "fix the bug")

	assertArgBefore(t, args, "-a", "exec")
	assertHasArgPair(t, args, "-a", "on-request")
	assertHasArgPair(t, args, "--sandbox", "workspace-write")
	assertHasArgPair(t, args, "--model", "gpt-5.3-codex")
	assertHasArgPair(t, args, "--profile", "team")
	assertHasArgPair(t, args, "--cd", workDir)
	assertHasArgPair(t, args, "--add-dir", extraDir)
	assertHasArgPair(t, args, "--add-dir", allowedDir)
	assertHasSequence(t, args, "exec", "resume", "thread-1", "--json", "--color", "never", "fix the bug")
}

func assertHasArg(t *testing.T, args []string, want string) {
	t.Helper()
	for _, arg := range args {
		if arg == want {
			return
		}
	}
	t.Fatalf("expected arg %q in %v", want, args)
}

func assertHasArgPair(t *testing.T, args []string, key, value string) {
	t.Helper()
	if !hasArgPair(args, key, value) {
		t.Fatalf("expected arg pair %q %q in %v", key, value, args)
	}
}

func hasArgPair(args []string, key, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func assertArgBefore(t *testing.T, args []string, arg, before string) {
	t.Helper()
	argIdx := indexOf(args, arg)
	beforeIdx := indexOf(args, before)
	if argIdx == -1 || beforeIdx == -1 || argIdx >= beforeIdx {
		t.Fatalf("expected %q before %q in %v", arg, before, args)
	}
}

func assertHasSequence(t *testing.T, args []string, seq ...string) {
	t.Helper()
	for i := 0; i <= len(args)-len(seq); i++ {
		match := true
		for j := range seq {
			if args[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Fatalf("expected sequence %v in %v", seq, args)
}

func indexOf(args []string, want string) int {
	for i, arg := range args {
		if arg == want {
			return i
		}
	}
	return -1
}

func TestBuildCodexTMuxArgs_UsesInteractiveResumeAndAllowedPaths(t *testing.T) {
	workDir := t.TempDir()
	extraDir := t.TempDir()
	allowedDir := t.TempDir()

	cfg := config.CodexConfig{
		Model:          "gpt-5.4",
		Transport:      "tmux",
		SandboxMode:    "workspace-write",
		Profile:        "team",
		WorkDir:        workDir,
		AdditionalDirs: []string{extraDir},
	}
	security := &config.SecurityConfig{
		PermissionMode: "strict",
		AllowedPaths:   []string{allowedDir},
	}

	args := buildCodexTMuxArgs(cfg, security, "thread-1", workDir)

	assertHasArgPair(t, args, "-a", "untrusted")
	assertHasArgPair(t, args, "--sandbox", "workspace-write")
	assertHasArgPair(t, args, "--model", "gpt-5.4")
	assertHasArgPair(t, args, "--profile", "team")
	assertHasArgPair(t, args, "-C", workDir)
	assertHasArgPair(t, args, "--add-dir", extraDir)
	assertHasArgPair(t, args, "--add-dir", allowedDir)
	assertHasSequence(t, args, "--no-alt-screen", "-a", "untrusted")
	assertHasSequence(t, args, "resume", "thread-1")
}
