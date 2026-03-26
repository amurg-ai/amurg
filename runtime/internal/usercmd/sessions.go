package usercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/pkg/hubapi"
	"github.com/amurg-ai/amurg/runtime/internal/wizard"
)

const defaultSessionListLimit = 20

type sessionsListOptions struct {
	hubURL               string
	token                string
	username             string
	password             string
	runtimeConfigPath    string
	profile              string
	jsonOutput           bool
	includeMissingHandle bool
	limit                int
}

func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List hub sessions",
	}
	cmd.AddCommand(newSessionsListCmd())
	return cmd
}

func newSessionsListCmd() *cobra.Command {
	opts := &sessionsListOptions{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent resumable sessions from the hub",
		Long: "List recent sessions from the hub API.\n\n" +
			"By default this filters to Claude Code sessions with a native handle so the returned " +
			"IDs work with `claude --resume <native_handle>`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionsList(cmd, opts)
		},
	}

	cmd.Flags().StringVar(&opts.hubURL, "hub-url", "", "hub base URL or WebSocket URL (env: AMURG_HUB_URL)")
	cmd.Flags().StringVar(&opts.token, "token", "", "hub bearer token (env: AMURG_TOKEN)")
	cmd.Flags().StringVar(&opts.username, "username", "", "hub username for builtin auth (env: AMURG_USERNAME)")
	cmd.Flags().StringVar(&opts.password, "password", "", "hub password for builtin auth (env: AMURG_PASSWORD)")
	cmd.Flags().StringVar(&opts.runtimeConfigPath, "config", "", "runtime config path used only to infer hub URL (default: ~/.amurg/config.json)")
	cmd.Flags().StringVar(&opts.profile, "profile", "claude-code", "limit results to a specific agent profile; empty means all profiles")
	cmd.Flags().BoolVar(&opts.includeMissingHandle, "include-missing-handle", false, "include sessions that do not have a native_handle yet")
	cmd.Flags().BoolVar(&opts.jsonOutput, "json", false, "emit JSON")
	cmd.Flags().IntVar(&opts.limit, "limit", defaultSessionListLimit, "maximum number of sessions to print after filtering")

	return cmd
}

func runSessionsList(cmd *cobra.Command, opts *sessionsListOptions) error {
	baseURL, err := resolveHubBaseURL(opts.hubURL, opts.runtimeConfigPath)
	if err != nil {
		return err
	}

	client, err := hubapi.New(baseURL, nil)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()

	token, err := resolveBearerToken(ctx, client, opts)
	if err != nil {
		return err
	}
	client.SetToken(token)

	sessions, err := client.ListSessions(ctx)
	if err != nil {
		return err
	}

	filtered := filterSessions(sessions, opts)

	if opts.jsonOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(filtered)
	}

	return writeHumanSessions(cmd, filtered, opts)
}

func resolveHubBaseURL(flagValue, configPath string) (string, error) {
	if env := strings.TrimSpace(os.Getenv("AMURG_HUB_URL")); flagValue == "" && env != "" {
		flagValue = env
	}
	if flagValue != "" {
		return hubapi.NormalizeBaseURL(flagValue)
	}

	path := configPath
	explicitConfig := path != ""
	if path == "" {
		path = wizard.DefaultConfigPath()
	}

	rawURL, err := readHubURLFromRuntimeConfig(path)
	if err == nil && rawURL != "" {
		return hubapi.NormalizeBaseURL(rawURL)
	}
	if explicitConfig && err != nil {
		return "", err
	}

	return "", fmt.Errorf("hub URL required: set --hub-url, AMURG_HUB_URL, or point --config at a runtime config")
}

func readHubURLFromRuntimeConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read runtime config %q: %w", path, err)
	}

	var cfg struct {
		Hub struct {
			URL string `json:"url"`
		} `json:"hub"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse runtime config %q: %w", path, err)
	}
	if cfg.Hub.URL == "" {
		return "", fmt.Errorf("runtime config %q does not contain hub.url", path)
	}
	return cfg.Hub.URL, nil
}

func resolveBearerToken(ctx context.Context, client *hubapi.Client, opts *sessionsListOptions) (string, error) {
	token := strings.TrimSpace(opts.token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("AMURG_TOKEN"))
	}
	if token != "" {
		return token, nil
	}

	username := strings.TrimSpace(opts.username)
	if username == "" {
		username = strings.TrimSpace(os.Getenv("AMURG_USERNAME"))
	}
	password := opts.password
	if password == "" {
		password = os.Getenv("AMURG_PASSWORD")
	}

	switch {
	case username == "" && password == "":
		return "", fmt.Errorf("authentication required: provide --token / AMURG_TOKEN or --username with --password")
	case username == "" || password == "":
		return "", fmt.Errorf("username and password must be provided together")
	}

	token, err := client.Login(ctx, username, password)
	if err != nil {
		return "", err
	}
	return token, nil
}

func filterSessions(sessions []hubapi.Session, opts *sessionsListOptions) []hubapi.Session {
	filtered := make([]hubapi.Session, 0, len(sessions))
	for _, sess := range sessions {
		if opts.profile != "" && sess.Profile != opts.profile {
			continue
		}
		if !opts.includeMissingHandle && sess.NativeHandle == "" {
			continue
		}
		filtered = append(filtered, sess)
		if opts.limit > 0 && len(filtered) >= opts.limit {
			break
		}
	}
	return filtered
}

func writeHumanSessions(cmd *cobra.Command, sessions []hubapi.Session, opts *sessionsListOptions) error {
	if len(sessions) == 0 {
		if opts.profile == "" {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "No matching sessions found.")
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "No matching %s sessions found.\n", opts.profile)
		return err
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NATIVE_HANDLE\tPROFILE\tCREATED_AT\tMESSAGE_COUNT"); err != nil {
		return err
	}
	for _, sess := range sessions {
		if _, err := fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%d\n",
			fallback(sess.NativeHandle, "-"),
			sess.Profile,
			sess.CreatedAt.Format(time.RFC3339),
			sess.MessageCount,
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func fallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
