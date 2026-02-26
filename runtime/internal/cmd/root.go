package cmd

import (
	"github.com/spf13/cobra"
)

var version = "dev"

// NewRootCmd creates the root cobra command for amurg-runtime.
// When invoked without a subcommand, it delegates to "run" for backward compat.
func NewRootCmd(v string) *cobra.Command {
	version = v

	root := &cobra.Command{
		Use:   "amurg-runtime",
		Short: "Amurg runtime — lightweight agent gateway",
		Long:  "Amurg runtime manages agent sessions and connects outbound to the hub.",
		// Bare invocation (no subcommand) behaves as "run".
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(cmd, args)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newRunCmd())
	root.AddCommand(newStartCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newLogsCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newAgentsCmd())
	root.AddCommand(newConfigCmd())

	root.PersistentFlags().StringP("config", "c", "", "path to config file")

	return root
}
