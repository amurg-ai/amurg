package adapter

import (
	"fmt"
	"os"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// resolveWorkDir determines the effective working directory for an agent
// session. It checks security overrides, validates the path exists, and
// falls back to the user's home directory if the configured path is missing
// or not set. Always returns a non-empty directory so the agent process
// never inherits the runtime daemon's arbitrary CWD.
func resolveWorkDir(profileWorkDir string, security *config.SecurityConfig) string {
	workDir := profileWorkDir
	if security != nil && security.Cwd != "" {
		workDir = security.Cwd
	}

	if workDir != "" {
		if info, err := os.Stat(workDir); err == nil && info.IsDir() {
			return workDir
		}
		fmt.Fprintf(os.Stderr, "WARNING: work_dir %q does not exist, falling back to home directory\n", workDir)
	}

	// No work_dir configured or configured path doesn't exist — use home.
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}
