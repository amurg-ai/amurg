package cmd

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/daemon"
	"github.com/amurg-ai/amurg/runtime/internal/wizard"
)

// runDefault implements the bare `amurg-runtime` (no subcommand) behavior:
//   - daemon running? → attach TUI
//   - no config? → run init wizard
//   - config exists, daemon stopped? → start daemon + attach TUI
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

	// Config exists, daemon not running — start daemon and attach dashboard.
	return startAndAttach(configPath)
}

// startAndAttach launches the runtime as a background daemon, waits for the
// IPC socket to become ready, and then attaches the dashboard TUI.
func startAndAttach(configPath string) error {
	// Validate config before starting.
	if _, err := config.Load(configPath); err != nil {
		return fmt.Errorf("invalid config: %w", err)
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

	fmt.Printf("Runtime started (PID %d)\n", child.Process.Pid)

	// Wait for the IPC socket to become available.
	socketPath := daemon.SocketPath()
	if err := waitForSocket(socketPath, 5*time.Second); err != nil {
		fmt.Printf("Warning: could not connect to dashboard (%v)\n", err)
		fmt.Printf("The runtime is running. View logs with: amurg-runtime logs\n")
		fmt.Printf("Attach later with: amurg-runtime attach\n")
		return nil
	}

	// Attach dashboard TUI.
	return runAttach(nil, nil)
}

// waitForSocket polls until the Unix socket accepts connections or the timeout expires.
func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket %s not ready after %s", path, timeout)
}
