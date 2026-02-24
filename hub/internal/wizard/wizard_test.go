package wizard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amurg-ai/amurg/hub/internal/config"
	"github.com/amurg-ai/amurg/pkg/cli"
)

func TestWizard_SQLite(t *testing.T) {
	input := strings.Join([]string{
		":9090",            // listen address
		"myadmin",          // admin username
		"secretpass",       // admin password
		"1",                // storage: sqlite (first option)
		"./data/amurg.db",  // sqlite path
		"my-runtime",       // runtime ID
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "hub-config.json")

	w := New(p)
	if err := w.Run(outputPath); err != nil {
		t.Fatalf("wizard.Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if cfg.Server.Addr != ":9090" {
		t.Errorf("server.addr = %q, want %q", cfg.Server.Addr, ":9090")
	}
	if cfg.Auth.JWTSecret == "" {
		t.Error("auth.jwt_secret is empty")
	}
	if len(cfg.Auth.JWTSecret) < 32 {
		t.Errorf("auth.jwt_secret length = %d, want >= 32", len(cfg.Auth.JWTSecret))
	}
	if cfg.Auth.InitialAdmin == nil {
		t.Fatal("auth.initial_admin is nil")
	}
	if cfg.Auth.InitialAdmin.Username != "myadmin" {
		t.Errorf("admin username = %q, want %q", cfg.Auth.InitialAdmin.Username, "myadmin")
	}
	if cfg.Auth.InitialAdmin.Password != "secretpass" {
		t.Errorf("admin password = %q, want %q", cfg.Auth.InitialAdmin.Password, "secretpass")
	}
	if cfg.Storage.Driver != "sqlite" {
		t.Errorf("storage.driver = %q, want %q", cfg.Storage.Driver, "sqlite")
	}
	if cfg.Storage.DSN != "./data/amurg.db" {
		t.Errorf("storage.dsn = %q, want %q", cfg.Storage.DSN, "./data/amurg.db")
	}
	if len(cfg.Auth.RuntimeTokens) != 1 {
		t.Fatalf("runtime_tokens count = %d, want 1", len(cfg.Auth.RuntimeTokens))
	}
	rt := cfg.Auth.RuntimeTokens[0]
	if rt.RuntimeID != "my-runtime" {
		t.Errorf("runtime_id = %q, want %q", rt.RuntimeID, "my-runtime")
	}
	if rt.Token == "" {
		t.Error("runtime token is empty")
	}
}

func TestWizard_Postgres(t *testing.T) {
	input := strings.Join([]string{
		":8080",     // listen address (default)
		"admin",     // admin username (default)
		"pass123",   // admin password
		"2",         // storage: postgres
		"postgres://amurg:pass@db:5432/amurg", // DSN
		"prod-runtime", // runtime ID
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	p := &cli.Prompter{In: strings.NewReader(input), Out: out}

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "hub-config.json")

	w := New(p)
	if err := w.Run(outputPath); err != nil {
		t.Fatalf("wizard.Run() error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if cfg.Storage.Driver != "postgres" {
		t.Errorf("storage.driver = %q, want %q", cfg.Storage.Driver, "postgres")
	}
	if cfg.Storage.DSN != "postgres://amurg:pass@db:5432/amurg" {
		t.Errorf("storage.dsn = %q, want %q", cfg.Storage.DSN, "postgres://amurg:pass@db:5432/amurg")
	}
}
