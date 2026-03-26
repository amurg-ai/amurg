package usercmd

import "github.com/spf13/cobra"

var version = "dev"

// NewRootCmd creates the root cobra command for the user-facing amurg CLI.
func NewRootCmd(v string) *cobra.Command {
	version = v

	root := &cobra.Command{
		Use:           "amurg",
		Short:         "Amurg user CLI",
		Long:          "Amurg queries the hub API for user-facing operations such as listing resumable sessions.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newSessionsCmd())
	root.AddCommand(newVersionCmd())

	return root
}
