package cmd

import (
	"github.com/spf13/cobra"
)

var version = "dev"

// NewRootCmd creates the root cobra command for amurg-runtime.
// When invoked without a subcommand in a TTY, it uses smart default logic:
// daemon running → attach, no config → init wizard, otherwise → run.
func NewRootCmd(v string) *cobra.Command {
	version = v

	root := &cobra.Command{
		Use:   "amurg-runtime",
		Short: "Amurg runtime — lightweight agent gateway",
		Long:  "Amurg runtime manages agent sessions and connects outbound to the hub.",
		// Bare invocation uses smart default logic.
		RunE: runDefault,
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
	root.AddCommand(newAttachCmd())

	root.PersistentFlags().StringP("config", "c", "", "path to config file")

	return root
}
