package adapter

import (
	"os"
	"testing"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

func TestResolveWorkDir_ConfiguredDir(t *testing.T) {
	dir := t.TempDir()
	got := resolveWorkDir(dir, nil)
	if got != dir {
		t.Errorf("expected %s, got %s", dir, got)
	}
}

func TestResolveWorkDir_SecurityOverrides(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	sec := &config.SecurityConfig{Cwd: dir2}
	got := resolveWorkDir(dir1, sec)
	if got != dir2 {
		t.Errorf("expected security cwd %s to override, got %s", dir2, got)
	}
}

func TestResolveWorkDir_EmptyFallsBackToHome(t *testing.T) {
	got := resolveWorkDir("", nil)
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home directory")
	}
	if got != home {
		t.Errorf("expected home dir %s when no work_dir, got %s", home, got)
	}
}

func TestResolveWorkDir_NonexistentFallsBackToHome(t *testing.T) {
	got := resolveWorkDir("/nonexistent/path/that/does/not/exist", nil)
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home directory")
	}
	if got != home {
		t.Errorf("expected home dir %s for nonexistent path, got %s", home, got)
	}
}

func TestResolveWorkDir_SecurityCwdNonexistentFallsBack(t *testing.T) {
	sec := &config.SecurityConfig{Cwd: "/nonexistent/security/cwd"}
	got := resolveWorkDir("", sec)
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home directory")
	}
	if got != home {
		t.Errorf("expected home dir %s for nonexistent security cwd, got %s", home, got)
	}
}

func TestResolveWorkDir_EmptySecurityDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	sec := &config.SecurityConfig{Cwd: ""}
	got := resolveWorkDir(dir, sec)
	if got != dir {
		t.Errorf("empty security cwd should not override; expected %s, got %s", dir, got)
	}
}
