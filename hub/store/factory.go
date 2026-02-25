package store

import (
	"fmt"

	"github.com/amurg-ai/amurg/hub/config"
)

// New creates a Store based on the configured storage driver.
func New(cfg config.StorageConfig) (Store, error) {
	switch cfg.Driver {
	case "postgres":
		return NewPostgres(cfg.DSN)
	case "sqlite", "":
		return NewSQLite(cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported storage driver: %q", cfg.Driver)
	}
}
