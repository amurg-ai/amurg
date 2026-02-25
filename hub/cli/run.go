package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/hub"
	"github.com/amurg-ai/amurg/hub/config"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run [config-file]",
		Short: "Start the hub (default when no subcommand is given)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runRun,
	}
}

func runRun(cmd *cobra.Command, args []string) error {
	configPath := resolveConfigPath(cmd, args, "hub-config.json")

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("error: %w", err)
	}

	// Set up structured logging.
	logLevel := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: logLevel}
	if cfg.Logging.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)

	// Create and run the hub.
	h, err := hub.New(cfg, hubOpts, logger)
	if err != nil {
		logger.Error("failed to initialize hub", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("amurg hub starting", "version", version, "config", configPath)

	if err := h.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("hub error", "error", err)
		os.Exit(1)
	}

	logger.Info("hub stopped")
	return nil
}

// resolveConfigPath returns the config file path from (in priority order):
// 1. Positional argument
// 2. --config / -c flag
// 3. Default value
func resolveConfigPath(cmd *cobra.Command, args []string, defaultPath string) string {
	if len(args) > 0 {
		return args[0]
	}
	if f := cmd.Flag("config"); f != nil && f.Changed {
		return f.Value.String()
	}
	if f := cmd.Root().PersistentFlags().Lookup("config"); f != nil && f.Changed {
		return f.Value.String()
	}
	return defaultPath
}
