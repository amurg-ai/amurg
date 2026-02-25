package cli

import (
	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/hub/wizard"
	"github.com/amurg-ai/amurg/pkg/cli"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactive setup wizard to generate a config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			output, _ := cmd.Flags().GetString("output")
			defaults, _ := cmd.Flags().GetBool("defaults")

			w := wizard.New(cli.DefaultPrompter())
			if defaults {
				return w.RunDefaults(output)
			}
			return w.Run(output)
		},
	}
	cmd.Flags().StringP("output", "o", "", "output config file path (default: ./amurg-hub.json)")
	cmd.Flags().Bool("defaults", false, "generate config non-interactively using env vars and secure defaults")
	return cmd
}
