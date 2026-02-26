package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

func newConfigCmd() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "View or edit runtime configuration",
		RunE:  runConfigShow, // default subcommand
	}
	configCmd.AddCommand(newConfigShowCmd())
	configCmd.AddCommand(newConfigEditCmd())
	return configCmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Display the current configuration",
		RunE:  runConfigShow,
	}
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open configuration in $EDITOR",
		RunE:  runConfigEdit,
	}
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	configPath := resolveConfigPath(cmd, nil, "runtime-config.json")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Mask the token for display.
	masked := *cfg
	masked.Hub.Token = maskToken(cfg.Hub.Token)

	data, err := json.MarshalIndent(masked, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Config: %s\n\n", configPath)
	_, _ = fmt.Fprintln(os.Stdout, string(data))
	return nil
}

func runConfigEdit(cmd *cobra.Command, args []string) error {
	configPath := resolveConfigPath(cmd, nil, "runtime-config.json")

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	editorCmd := exec.Command(editor, configPath)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr
	return editorCmd.Run()
}

func maskToken(token string) string {
	if len(token) <= 8 {
		return strings.Repeat("*", len(token))
	}
	return token[:4] + strings.Repeat("*", len(token)-8) + token[len(token)-4:]
}
