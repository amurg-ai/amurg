package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/daemon"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start [config-file]",
		Short: "Start the runtime as a background process",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runStart,
	}
}

func runStart(cmd *cobra.Command, args []string) error {
	configPath := resolveConfigPath(cmd, args, "runtime-config.json")

	// Validate config before starting.
	if _, err := config.Load(configPath); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Check if already running.
	pid, _ := daemon.ReadPID()
	if pid > 0 && daemon.IsRunning(pid) {
		return fmt.Errorf("runtime is already running (PID %d)", pid)
	}

	// Find our own binary.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Open log file for output.
	logFile, err := daemon.OpenLogFile()
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	// Launch the runtime in the background.
	child := exec.Command(exe, "run", configPath)
	child.Stdout = logFile
	child.Stderr = logFile
	child.SysProcAttr = daemon.DetachSysProcAttr()

	if err := child.Start(); err != nil {
		return fmt.Errorf("start runtime: %w", err)
	}

	if err := daemon.WritePID(child.Process.Pid); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Runtime started (PID %d)\n", child.Process.Pid)
	_, _ = fmt.Fprintf(os.Stdout, "  Config: %s\n", configPath)
	_, _ = fmt.Fprintf(os.Stdout, "  Logs:   %s\n", daemon.LogPath())
	return nil
}
