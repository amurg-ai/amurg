package usercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amurg-ai/amurg/pkg/hubapi"
	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/wizard"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	var configPath, dir string
	var yolo, skipSandbox, jsonOut bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "start <agent> [-- native arguments...]",
		Short: "Start an agent here and pick up its separate session in Cloud",
		Long:  "Start Codex or OpenCode in the current folder using your registered runtime.\nThe runtime launches a separate persistent terminal, visible in Cloud under the\naccount that registered this runtime. No browser token is needed.\n\nUse --yolo for unattended execution, --skip-sandbox for Codex, and -- to\npass native flags or resume arguments. This requires an updated hub and runtime.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 && cmd.ArgsLenAtDash() != 1 {
				return fmt.Errorf("put native agent arguments after --")
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			candidates := make([]hubapi.Agent, 0, len(cfg.Agents))
			for _, a := range cfg.Agents {
				candidates = append(candidates, hubapi.Agent{ID: a.ID, Name: a.Name, Profile: a.Profile, RuntimeID: cfg.Runtime.ID})
			}
			agent, err := resolveStartAgent(candidates, args[0])
			if err != nil {
				return err
			}
			if agent.Profile != "codex" && agent.Profile != "opencode" {
				return fmt.Errorf("start currently supports codex and opencode")
			}
			if skipSandbox && agent.Profile != "codex" {
				return fmt.Errorf("--skip-sandbox is only supported for Codex; use --yolo for OpenCode")
			}
			if dir == "" {
				dir, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			dir, err = filepath.Abs(dir)
			if err != nil {
				return err
			}
			dir, err = filepath.EvalSymlinks(dir)
			if err != nil {
				return err
			}
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("working directory does not exist: %s", dir)
			}
			client, err := hubapi.New(cfg.Hub.URL, nil)
			if err != nil {
				return err
			}
			client.SetToken(cfg.Hub.Token)
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			sess, err := client.StartRuntimeSession(ctx, agent.ID, protocol.SessionLaunch{WorkDir: dir, Args: args[1:], Yolo: yolo, SkipSandbox: skipSandbox})
			if err != nil {
				return fmt.Errorf("start Cloud session: %w", err)
			}
			id := sess.ID
			if id == "" {
				return fmt.Errorf("hub returned no session ID")
			}
			for sess.State == "creating" || sess.State == "queued" {
				select {
				case <-ctx.Done():
					return fmt.Errorf("session %s was created but startup is unconfirmed: %w", id, ctx.Err())
				case <-time.After(200 * time.Millisecond):
				}
				sess, err = client.GetRuntimeSession(ctx, id)
				if err != nil {
					return fmt.Errorf("session %s was created but status could not be checked: %w", id, err)
				}
			}
			if sess.State != "active" && sess.State != "idle" {
				return fmt.Errorf("session %s did not start (state %s); check runtime logs", id, sess.State)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(sess)
			}
			base, _ := hubapi.NormalizeBaseURL(cfg.Hub.URL)
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Started %s in %s\nSession: %s\nOpen %s and select this %s session.\n", agent.Profile, dir, sess.ID, base, agent.Name)
			return err
		},
	}
	cmd.Flags().StringVar(&configPath, "config", wizard.DefaultConfigPath(), "runtime configuration")
	cmd.Flags().StringVarP(&dir, "cwd", "C", "", "working directory (default: current directory)")
	cmd.Flags().BoolVar(&yolo, "yolo", false, "auto-approve agent actions (Codex also disables sandboxing)")
	cmd.Flags().BoolVar(&skipSandbox, "skip-sandbox", false, "disable Codex sandbox and approval prompts")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit session JSON")
	cmd.Flags().DurationVar(&timeout, "timeout", 45*time.Second, "maximum startup wait")
	return cmd
}

func resolveStartAgent(agents []hubapi.Agent, query string) (hubapi.Agent, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return hubapi.Agent{}, fmt.Errorf("agent is required")
	}
	for _, match := range []func(hubapi.Agent) bool{
		func(a hubapi.Agent) bool { return a.ID == query },
		func(a hubapi.Agent) bool { return a.Name == query },
		func(a hubapi.Agent) bool { return a.Profile == query },
		func(a hubapi.Agent) bool { return strings.HasPrefix(a.ID, query) || strings.HasPrefix(a.Name, query) },
	} {
		var matches []hubapi.Agent
		for _, a := range agents {
			if match(a) {
				matches = append(matches, a)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return hubapi.Agent{}, fmt.Errorf("agent %q is ambiguous; use its exact configured ID", query)
		}
	}
	return hubapi.Agent{}, fmt.Errorf("no configured agent matches %q", query)
}
