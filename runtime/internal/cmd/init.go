package cmd

import (
	"github.com/spf13/cobra"

	tuiwizard "github.com/amurg-ai/amurg/runtime/internal/tui/wizard"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactive setup wizard to generate a config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			output, _ := cmd.Flags().GetString("output")
			systemd, _ := cmd.Flags().GetBool("systemd")
			plain, _ := cmd.Flags().GetBool("plain")

			configPath, startNow, err := tuiwizard.Run(output, systemd, plain)
			if err != nil {
				return err
			}

			if startNow {
				return startAndAttach(configPath)
			}

			return nil
		},
	}
	cmd.Flags().StringP("output", "o", "", "output config file path (default: ~/.amurg/config.json)")
	cmd.Flags().Bool("systemd", false, "also generate a systemd unit file")
	cmd.Flags().Bool("plain", false, "use plain-text wizard instead of TUI")
	return cmd
}
