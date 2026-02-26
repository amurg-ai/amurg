package adapter

import (
	"fmt"
	"os"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// resolveWorkDir determines the effective working directory for an agent
// session. It checks security overrides, validates the path exists, and
// falls back to the user's home directory with a warning if the configured
// path is missing.
func resolveWorkDir(profileWorkDir string, security *config.SecurityConfig) string {
	workDir := profileWorkDir
	if security != nil && security.Cwd != "" {
		workDir = security.Cwd
	}
	if workDir == "" {
		return ""
	}

	if info, err := os.Stat(workDir); err == nil && info.IsDir() {
		return workDir
	}

	// Configured path doesn't exist — fall back gracefully.
	if home, err := os.UserHomeDir(); err == nil {
		fmt.Fprintf(os.Stderr, "WARNING: work_dir %q does not exist, falling back to %s\n", workDir, home)
		return home
	}
	fmt.Fprintf(os.Stderr, "WARNING: work_dir %q does not exist, using current directory\n", workDir)
	return ""
}
