package cli

import (
	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/hub"
)

var (
	version = "dev"
	hubOpts hub.Options
)

// NewRootCmd creates the root cobra command for amurg-hub.
// When invoked without a subcommand, it delegates to "run" for backward compat.
func NewRootCmd(v string, opts hub.Options) *cobra.Command {
	version = v
	hubOpts = opts

	root := &cobra.Command{
		Use:   "amurg-hub",
		Short: "Amurg hub — central agent control plane",
		Long:  "Amurg hub handles authentication, message routing, session persistence, and serves the web UI.",
		// Bare invocation (no subcommand) behaves as "run".
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(cmd, args)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newRunCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newVersionCmd())

	root.PersistentFlags().StringP("config", "c", "", "path to config file")

	return root
}
