package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/wizard"
)

func loadConfigWithGuidance(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, formatConfigLoadError(path, err)
	}
	return cfg, nil
}

func formatConfigLoadError(path string, err error) error {
	var b strings.Builder

	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(&b, "runtime config not found at %s", path)
		b.WriteString("\n\nInitialize it with:\n")
	} else {
		fmt.Fprintf(&b, "invalid runtime config at %s: %v", path, err)
		b.WriteString("\n\nRepair it with:\n")
		fmt.Fprintf(&b, "  amurg-runtime config edit --config %s\n", quoteArg(path))
	}

	fmt.Fprintf(&b, "  %s\n", initCommand(path, false))
	fmt.Fprintf(&b, "  %s\n", initCommand(path, true))

	defaultPath := wizard.DefaultConfigPath()
	if path != defaultPath {
		fmt.Fprintf(&b, "\nDefault config path: %s", defaultPath)
		fmt.Fprintf(&b, "\nUse --config %s with runtime commands to keep using this file.", quoteArg(path))
	}

	return errors.New(b.String())
}

func initCommand(path string, plain bool) string {
	parts := []string{"amurg-runtime", "init"}
	if plain {
		parts = append(parts, "--plain")
	}
	if path != "" && path != wizard.DefaultConfigPath() {
		parts = append(parts, "--output", quoteArg(path))
	}
	return strings.Join(parts, " ")
}

func quoteArg(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\"'\\") {
		return strconv.Quote(value)
	}
	return value
}
