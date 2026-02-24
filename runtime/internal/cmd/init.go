package cmd

import (
	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/pkg/cli"
	"github.com/amurg-ai/amurg/runtime/internal/wizard"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactive setup wizard to generate a config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			output, _ := cmd.Flags().GetString("output")
			systemd, _ := cmd.Flags().GetBool("systemd")

			w := wizard.New(cli.DefaultPrompter())
			return w.Run(output, systemd)
		},
	}
	cmd.Flags().StringP("output", "o", "", "output config file path (default: ./amurg-runtime.json)")
	cmd.Flags().Bool("systemd", false, "also generate a systemd unit file")
	return cmd
}
