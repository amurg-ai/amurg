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

	if _, err := dashboard.Attach(socketPath); err != nil {
		return fmt.Errorf("attach failed: %w", err)
	}

	fmt.Println("Runtime continues in the background.")
	fmt.Println("Re-attach: amurg-runtime  |  Stop: amurg-runtime stop")
	return nil
}
