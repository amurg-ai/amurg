// Package wizard provides an interactive setup wizard for the amurg hub.
package wizard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/amurg-ai/amurg/hub/internal/config"
	"github.com/amurg-ai/amurg/pkg/cli"
)

// Wizard drives the interactive hub config setup.
type Wizard struct {
	p *cli.Prompter
}

// New creates a Wizard using the given Prompter.
func New(p *cli.Prompter) *Wizard {
	return &Wizard{p: p}
}

// Run executes the interactive wizard and writes the config file.
func (w *Wizard) Run(outputPath string) error {
	_, _ = fmt.Fprintln(w.p.Out)
	_, _ = fmt.Fprintln(w.p.Out, "  Amurg Hub — Configuration Wizard")
	_, _ = fmt.Fprintln(w.p.Out, strings.Repeat("─", 38))
	_, _ = fmt.Fprintln(w.p.Out)

	cfg := &config.Config{}

	// JWT secret — auto-generated.
	secret, err := config.GenerateRandomSecret()
	if err != nil {
		return fmt.Errorf("generate JWT secret: %w", err)
	}
	cfg.Auth.JWTSecret = secret
	_, _ = fmt.Fprintf(w.p.Out, "  Generated JWT secret: %s\n\n", secret)

	// Server settings.
	_, _ = fmt.Fprintln(w.p.Out, "Server")
	cfg.Server.Addr = w.p.Ask("  Listen address", ":8080")
	_, _ = fmt.Fprintln(w.p.Out)

	// Admin user.
	_, _ = fmt.Fprintln(w.p.Out, "Admin User")
	adminUser := w.p.Ask("  Username", "admin")
	adminPass := w.p.AskPassword("  Password")
	cfg.Auth.InitialAdmin = &config.InitialAdmin{
		Username: adminUser,
		Password: adminPass,
	}
	_, _ = fmt.Fprintln(w.p.Out)

	// Storage.
	_, _ = fmt.Fprintln(w.p.Out, "Storage")
	driver := w.p.Choose("  Database driver", []string{"sqlite", "postgres"}, 0)
	cfg.Storage.Driver = driver

	switch driver {
	case "sqlite":
		cfg.Storage.DSN = w.p.Ask("  SQLite database path", "amurg.db")
	case "postgres":
		cfg.Storage.DSN = w.p.Ask("  PostgreSQL DSN", "postgres://user:pass@localhost:5432/amurg?sslmode=disable")
	}
	_, _ = fmt.Fprintln(w.p.Out)

	// Runtime token.
	_, _ = fmt.Fprintln(w.p.Out, "Runtime Authentication")
	runtimeID := w.p.Ask("  Runtime ID to authorize", "default-runtime")
	runtimeToken, err := generateToken()
	if err != nil {
		return fmt.Errorf("generate runtime token: %w", err)
	}
	cfg.Auth.RuntimeTokens = []config.RuntimeTokenEntry{
		{RuntimeID: runtimeID, Token: runtimeToken, Name: "Default Runtime"},
	}

	_, _ = fmt.Fprintln(w.p.Out)
	_, _ = fmt.Fprintln(w.p.Out, "  Copy these values to your runtime config:")
	_, _ = fmt.Fprintf(w.p.Out, "    Runtime ID:  %s\n", runtimeID)
	_, _ = fmt.Fprintf(w.p.Out, "    Token:       %s\n", runtimeToken)
	_, _ = fmt.Fprintln(w.p.Out)

	// Output path.
	if outputPath == "" {
		outputPath = w.p.Ask("Config file output path", "./amurg-hub.json")
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(outputPath, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	_, _ = fmt.Fprintf(w.p.Out, "\n  Config written to %s\n", outputPath)
	_, _ = fmt.Fprintln(w.p.Out)
	_, _ = fmt.Fprintln(w.p.Out, "  Next steps:")
	_, _ = fmt.Fprintf(w.p.Out, "    amurg-hub run %s\n\n", outputPath)

	return nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
