package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/runtime/internal/daemon"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background runtime process",
		RunE:  runStop,
	}
}

func runStop(cmd *cobra.Command, args []string) error {
	pid, err := daemon.ReadPID()
	if err != nil {
		return fmt.Errorf("read PID file: %w", err)
	}
	if pid == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "Runtime is not running (no PID file)")
		return nil
	}

	if !daemon.IsRunning(pid) {
		_ = daemon.RemovePID()
		_, _ = fmt.Fprintf(os.Stdout, "Runtime is not running (stale PID %d removed)\n", pid)
		return nil
	}

	_, _ = fmt.Fprintf(os.Stdout, "Stopping runtime (PID %d)...\n", pid)
	if err := daemon.StopProcess(pid, 5*time.Second); err != nil {
		return err
	}

	_ = daemon.RemovePID()
	_, _ = fmt.Fprintln(os.Stdout, "Runtime stopped")
	return nil
}
