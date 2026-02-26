package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/runtime/internal/daemon"
	"github.com/amurg-ai/amurg/runtime/internal/tui/dashboard"
)

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach",
		Short: "Attach TUI dashboard to a running runtime daemon",
		RunE:  runAttach,
	}
}

func runAttach(cmd *cobra.Command, args []string) error {
	socketPath := daemon.SocketPath()

	detached, err := dashboard.Attach(socketPath)
	if err != nil {
		return fmt.Errorf("attach failed: %w", err)
	}

	if detached {
		fmt.Println("Detached from runtime. Daemon continues running.")
		fmt.Println("Re-attach with: amurg-runtime attach")
	}

	return nil
}
