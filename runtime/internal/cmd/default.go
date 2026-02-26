package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/amurg-ai/amurg/runtime/internal/daemon"
	"github.com/amurg-ai/amurg/runtime/internal/wizard"
)

// runDefault implements the bare `amurg-runtime` (no subcommand) behavior:
//   - daemon running? → attach TUI
//   - no config? → run init wizard
//   - config exists, daemon stopped? → start runtime (run)
func runDefault(cmd *cobra.Command, args []string) error {
	// Only use smart default logic if running in a TTY.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return runRun(cmd, args)
	}

	// Check if a daemon is already running.
	pid, _ := daemon.ReadPID()
	if pid != 0 && daemon.IsRunning(pid) {
		// Daemon is running — attach to it.
		return runAttach(cmd, args)
	}

	// Check if config exists.
	configPath := resolveConfigPath(cmd, args, "")
	if configPath == "" {
		configPath = wizard.DefaultConfigPath()
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// No config — run init wizard.
		initCmd := newInitCmd()
		initCmd.SetContext(cmd.Context())
		return initCmd.RunE(initCmd, nil)
	}

	// Config exists, daemon not running — start runtime.
	return runRun(cmd, args)
}
