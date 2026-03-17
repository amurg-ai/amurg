package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatConfigLoadErrorNotFound(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)

	configPath := filepath.Join(t.TempDir(), "custom config.json")
	err := formatConfigLoadError(configPath, fmt.Errorf("read config: %w", os.ErrNotExist))
	if err == nil {
		t.Fatal("expected an error")
	}

	msg := err.Error()
	assertContains(t, msg, "runtime config not found at "+configPath)
	assertContains(t, msg, `amurg-runtime init --output "`)
	assertContains(t, msg, `amurg-runtime init --plain --output "`)
	assertContains(t, msg, "Default config path: "+filepath.Join(home, ".amurg", "config.json"))
	assertContains(t, msg, `Use --config "`)
}

func TestFormatConfigLoadErrorInvalid(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)

	configPath := filepath.Join(t.TempDir(), "runtime-config.json")
	err := formatConfigLoadError(configPath, fmt.Errorf("validate config: hub.url is required"))
	if err == nil {
		t.Fatal("expected an error")
	}

	msg := err.Error()
	assertContains(t, msg, "invalid runtime config at "+configPath+": validate config: hub.url is required")
	assertContains(t, msg, "amurg-runtime config edit --config "+configPath)
	assertContains(t, msg, "amurg-runtime init --output "+configPath)
	assertContains(t, msg, "amurg-runtime init --plain --output "+configPath)
}

func TestRunStartMissingConfigShowsSetupHelp(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)

	configPath := filepath.Join(t.TempDir(), "missing.json")
	err := runStart(newStartCmd(), []string{configPath})
	if err == nil {
		t.Fatal("expected an error")
	}

	msg := err.Error()
	assertContains(t, msg, "runtime config not found at "+configPath)
	assertContains(t, msg, "amurg-runtime init --plain --output "+configPath)
}

func TestRunRunInvalidConfigShowsRepairHelp(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "runtime-config.json")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := runRun(newRunCmd(), []string{configPath})
	if err == nil {
		t.Fatal("expected an error")
	}

	msg := err.Error()
	assertContains(t, msg, "invalid runtime config at "+configPath)
	assertContains(t, msg, "amurg-runtime config edit --config "+configPath)
	assertContains(t, msg, "amurg-runtime init --plain --output "+configPath)
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}
