// Package hub is the main orchestrator that ties all hub components together.
package hub

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/amurg-ai/amurg/hub/internal/api"
	"github.com/amurg-ai/amurg/hub/internal/auth"
	"github.com/amurg-ai/amurg/hub/internal/config"
	"github.com/amurg-ai/amurg/hub/internal/router"
	"github.com/amurg-ai/amurg/hub/internal/store"
)

// Hub is the main hub process.
type Hub struct {
	cfg          *config.Config
	store        store.Store
	authProvider auth.Provider
	router       *router.Router
	api          *api.Server
	logger       *slog.Logger
}

// New creates a new hub from configuration.
func New(cfg *config.Config, logger *slog.Logger) (*Hub, error) {
	// Initialize storage.
	db, err := store.New(cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("init storage: %w", err)
	}

	// Create auth provider based on config.
	authProvider, err := auth.NewProvider(cfg.Auth, db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init auth provider: %w", err)
	}

	// Bootstrap (creates admin user for builtin provider).
	if err := authProvider.Bootstrap(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("bootstrap auth: %w", err)
	}

	// Get LoginProvider.
	var loginProvider auth.LoginProvider
	if lp, ok := authProvider.(auth.LoginProvider); ok {
		loginProvider = lp
	}

	// Get RuntimeAuthProvider.
	var runtimeAuth auth.RuntimeAuthProvider
	if ra, ok := authProvider.(auth.RuntimeAuthProvider); ok {
		runtimeAuth = ra
	}

	// Initialize router.
	rt := router.New(db, authProvider, runtimeAuth, logger, router.Options{
		TurnBased:          cfg.Session.TurnBased,
		MaxPerUser:         cfg.Session.MaxPerUser,
		AllowedOrigins:     cfg.Server.AllowedOrigins,
		MaxClientMsgBytes:  cfg.Session.MaxMessageBytes,
		FileStoragePath:    cfg.Server.FileStoragePath,
		MaxFileBytes:       cfg.Server.MaxFileBytes,
	})

	// Initialize API server.
	apiSrv := api.NewServer(db, authProvider, loginProvider, runtimeAuth, rt, cfg, logger)

	h := &Hub{
		cfg:          cfg,
		store:        db,
		authProvider: authProvider,
		router:       rt,
		api:          apiSrv,
		logger:       logger.With("component", "hub"),
	}

	// Startup validation warnings (only for builtin provider).
	if authProvider.Name() == "builtin" {
		if len(cfg.Auth.JWTSecret) < 32 {
			logger.Warn("JWT secret is shorter than 32 characters — use a stronger secret in production")
		}
		if cfg.Auth.InitialAdmin != nil &&
			cfg.Auth.InitialAdmin.Username == "admin" && cfg.Auth.InitialAdmin.Password == "admin" {
			logger.Warn("default admin credentials detected (admin/admin) — change immediately in production")
		}
	}
	for _, origin := range cfg.Server.AllowedOrigins {
		if origin == "*" {
			logger.Warn("CORS allowed_origins contains wildcard '*' — restrict to specific origins in production")
			break
		}
	}

	if cfg.Server.UIStaticDir != "" {
		if _, err := os.Stat(cfg.Server.UIStaticDir); os.IsNotExist(err) {
			logger.Warn("UI static directory does not exist", "path", cfg.Server.UIStaticDir)
		}
	}

	return h, nil
}

// Run starts the hub HTTP server and blocks until the context is canceled.
func (h *Hub) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    h.cfg.Server.Addr,
		Handler: h.api.Handler(),
	}

	// Build per-profile idle timeout map.
	profileTimeouts := make(map[string]time.Duration, len(h.cfg.Session.ProfileIdleTimeouts))
	for profile, d := range h.cfg.Session.ProfileIdleTimeouts {
		profileTimeouts[profile] = d.Duration
	}

	// Start idle reaper.
	h.router.StartIdleReaper(ctx, h.cfg.Session.IdleTimeout.Duration, profileTimeouts)

	// Start rate limiter cleanup tasks.
	h.api.StartBackgroundTasks(ctx)

	// Start retention purger.
	if h.cfg.Storage.Retention.Duration > 0 {
		go h.runRetentionPurger(ctx, h.cfg.Storage.Retention.Duration, h.cfg.Storage.AuditRetention.Duration)
	}

	errCh := make(chan error, 1)
	go func() {
		h.logger.Info("hub listening", "addr", h.cfg.Server.Addr)
		if h.cfg.Server.TLSCert != "" && h.cfg.Server.TLSKey != "" {
			errCh <- srv.ListenAndServeTLS(h.cfg.Server.TLSCert, h.cfg.Server.TLSKey)
		} else {
			h.logger.Warn("TLS not configured, running without encryption (development only)")
			errCh <- srv.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		h.logger.Info("shutting down hub gracefully")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			h.logger.Warn("graceful shutdown failed, forcing close", "error", err)
			_ = srv.Close()
		} else {
			h.logger.Info("http server stopped gracefully")
		}

		h.logger.Info("closing store")
		_ = h.store.Close()
		h.logger.Info("shutdown complete")
		return ctx.Err()

	case err := <-errCh:
		_ = h.store.Close()
		return err
	}
}

func (h *Hub) runRetentionPurger(ctx context.Context, retention, auditRetention time.Duration) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msgCutoff := time.Now().Add(-retention)
			if n, err := h.store.PurgeOldMessages(ctx, msgCutoff); err != nil {
				h.logger.Warn("retention purge: messages failed", "error", err)
			} else if n > 0 {
				h.logger.Info("retention purge: deleted old messages", "count", n)
			}
			auditCutoff := time.Now().Add(-auditRetention)
			if n, err := h.store.PurgeOldAuditEvents(ctx, auditCutoff); err != nil {
				h.logger.Warn("retention purge: audit events failed", "error", err)
			} else if n > 0 {
				h.logger.Info("retention purge: deleted old audit events", "count", n)
			}
		}
	}
}
