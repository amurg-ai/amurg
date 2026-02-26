package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/daemon"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show runtime status",
		RunE:  runStatus,
	}
}

func runStatus(cmd *cobra.Command, args []string) error {
	pid, _ := daemon.ReadPID()

	if pid == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "Status:  stopped (no PID file)")
		return nil
	}

	if !daemon.IsRunning(pid) {
		_, _ = fmt.Fprintf(os.Stdout, "Status:  stopped (stale PID %d)\n", pid)
		return nil
	}

	_, _ = fmt.Fprintf(os.Stdout, "Status:  running\n")
	_, _ = fmt.Fprintf(os.Stdout, "PID:     %d\n", pid)
	_, _ = fmt.Fprintf(os.Stdout, "Logs:    %s\n", daemon.LogPath())

	// Try to show config info.
	configPath := resolveConfigPath(cmd, nil, "runtime-config.json")
	cfg, err := config.Load(configPath)
	if err == nil {
		_, _ = fmt.Fprintf(os.Stdout, "Config:  %s\n", configPath)
		_, _ = fmt.Fprintf(os.Stdout, "Hub:     %s\n", cfg.Hub.URL)
		_, _ = fmt.Fprintf(os.Stdout, "Runtime: %s\n", cfg.Runtime.ID)
		_, _ = fmt.Fprintf(os.Stdout, "Agents:  %d configured\n", len(cfg.Agents))
	}

	return nil
}
