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
	"github.com/amurg-ai/amurg/runtime/internal/daemon"
	"github.com/amurg-ai/amurg/runtime/internal/eventbus"
	"github.com/amurg-ai/amurg/runtime/internal/ipc"
	"github.com/amurg-ai/amurg/runtime/internal/runtime"
	"github.com/amurg-ai/amurg/runtime/internal/wizard"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [config-file]",
		Short: "Start the runtime (default when no subcommand is given)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runRun,
	}
	cmd.Flags().Bool("no-tui", false, "disable TUI dashboard (headless JSON mode)")
	return cmd
}

func runRun(cmd *cobra.Command, args []string) error {
	configPath := resolveConfigPath(cmd, args, "runtime-config.json")

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("error: %w", err)
	}

	// Set up structured logging with event bus tee.
	logLevel := slog.LevelInfo
	switch cfg.Runtime.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	bus := eventbus.New()

	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	teeHandler := eventbus.NewSlogHandler(jsonHandler, bus)
	logger := slog.New(teeHandler)

	// Create and run the runtime.
	rt := runtime.New(cfg, logger, bus)

	// Start IPC server (non-fatal if it fails).
	socketPath := daemon.SocketPath()
	ipcServer := ipc.NewServer(socketPath, rt, bus, logger)
	if err := ipcServer.Start(); err != nil {
		logger.Warn("IPC server failed to start", "error", err)
	} else {
		defer func() { _ = ipcServer.Close() }()
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

	logger.Info("amurg runtime starting", "version", version, "config", configPath)

	if err := rt.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("runtime error", "error", err)
		os.Exit(1)
	}

	logger.Info("runtime stopped")
	bus.Close()
	return nil
}

// resolveConfigPath returns the config file path from (in priority order):
// 1. Positional argument
// 2. --config / -c flag
// 3. ~/.amurg/config.json (if it exists)
// 4. Default value (runtime-config.json in CWD)
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
	// Check default config location (~/.amurg/config.json).
	if p := wizard.DefaultConfigPath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return defaultPath
}
