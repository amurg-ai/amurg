package auth

import (
	"fmt"

	"github.com/amurg-ai/amurg/hub/internal/config"
	"github.com/amurg-ai/amurg/hub/internal/store"
)

// NewProvider creates an auth Provider based on configuration.
func NewProvider(cfg config.AuthConfig, s store.Store) (Provider, error) {
	if cfg.Provider != "" && cfg.Provider != "builtin" {
		return nil, fmt.Errorf("unknown auth provider: %q", cfg.Provider)
	}
	return NewService(s, cfg), nil
}
