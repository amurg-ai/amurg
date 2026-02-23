package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/runtime"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "runtime-config.json", "path to runtime configuration file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("amurg-runtime", version)
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
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

	// Handle shutdown signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("amurg runtime starting", "version", version, "config", *configPath)

	if err := rt.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("runtime error", "error", err)
		os.Exit(1)
	}

	logger.Info("runtime stopped")
}
