package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/runtime"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run [config-file]",
		Short: "Start the runtime (default when no subcommand is given)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runRun,
	}
}

func runRun(cmd *cobra.Command, args []string) error {
	configPath := resolveConfigPath(cmd, args, "runtime-config.json")

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("error: %w", err)
	}

	// Set up structured logging.
	logLevel := slog.LevelInfo
	switch cfg.Runtime.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))

	// Create and run the runtime.
	rt := runtime.New(cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("amurg runtime starting", "version", version, "config", configPath)

	if err := rt.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("runtime error", "error", err)
		os.Exit(1)
	}

	logger.Info("runtime stopped")
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
	// Check parent (root) persistent flags too.
	if f := cmd.Root().PersistentFlags().Lookup("config"); f != nil && f.Changed {
		return f.Value.String()
	}
	return defaultPath
}
