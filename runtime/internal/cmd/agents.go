package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/pkg/cli"
	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/wizard"
)

func newAgentsCmd() *cobra.Command {
	agentsCmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage configured agents",
		RunE:  runAgentsList, // default subcommand
	}
	agentsCmd.AddCommand(newAgentsListCmd())
	agentsCmd.AddCommand(newAgentsAddCmd())
	agentsCmd.AddCommand(newAgentsRemoveCmd())
	return agentsCmd
}

func newAgentsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured agents",
		RunE:  runAgentsList,
	}
}

func newAgentsAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add a new agent interactively",
		RunE:  runAgentsAdd,
	}
}

func newAgentsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove an agent by ID",
		Args:  cobra.ExactArgs(1),
		RunE:  runAgentsRemove,
	}
}

func runAgentsList(cmd *cobra.Command, args []string) error {
	configPath := resolveConfigPath(cmd, nil, "runtime-config.json")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if len(cfg.Agents) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "No agents configured.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tPROFILE\tNAME")
	for _, ep := range cfg.Agents {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", ep.ID, ep.Profile, ep.Name)
	}
	return w.Flush()
}

func runAgentsAdd(cmd *cobra.Command, args []string) error {
	configPath := resolveConfigPath(cmd, nil, "runtime-config.json")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	w := wizard.New(cli.DefaultPrompter())
	ep := w.ConfigureAgent(len(cfg.Agents))
	cfg.Agents = append(cfg.Agents, ep)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Agent %q added to %s\n", ep.ID, configPath)
	return nil
}

func runAgentsRemove(cmd *cobra.Command, args []string) error {
	targetID := args[0]
	configPath := resolveConfigPath(cmd, nil, "runtime-config.json")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	found := false
	filtered := cfg.Agents[:0]
	for _, ep := range cfg.Agents {
		if ep.ID == targetID {
			found = true
			continue
		}
		filtered = append(filtered, ep)
	}

	if !found {
		return fmt.Errorf("agent %q not found", targetID)
	}

	cfg.Agents = filtered

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Agent %q removed from %s\n", targetID, configPath)
	return nil
}
