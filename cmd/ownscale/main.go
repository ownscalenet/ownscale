package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/auth/oidcprovider"
	"github.com/ownscalenet/ownscale/internal/config"
	"github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/dockerctl"
	"github.com/ownscalenet/ownscale/internal/handlers"
	"github.com/ownscalenet/ownscale/internal/logging"
	"github.com/ownscalenet/ownscale/internal/server"
	"github.com/ownscalenet/ownscale/internal/webui"
)

// shutdownTimeout bounds how long we wait for in-flight requests to finish
// before forcing the process to exit.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		// No config/logger may be available yet (e.g. run failed while
		// loading them), so fall back to a minimal default logger.
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("fatal", "error", err)
		os.Exit(1)
	}
}

// run wires up and serves the admin panel, returning (rather than calling
// os.Exit) on failure so every deferred cleanup (closing the DB pool, the
// Docker client, etc.) always runs.
func run() error {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config file (default: "+config.DefaultPath+")")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	logger, err := logging.New(cfg.Log)
	if err != nil {
		return fmt.Errorf("build logger: %w", err)
	}

	pool, err := db.Connect(cfg.Database())
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}
	defer pool.Close()

	ctl, err := dockerctl.New(cfg.DataDir, cfg.CaddyLocalCerts != "", logger)
	if err != nil {
		return fmt.Errorf("docker init: %w", err)
	}
	defer ctl.Close()
	ctx := context.Background()
	if err := ctl.StreamAllLogs(ctx); err != nil {
		logger.Warn("reattach organization container logs", "error", err)
	}
	// Re-assert Caddy's network alias for BaseDomain on every startup: it's
	// set imperatively via the Docker API (not declared in
	// docker-compose.yml), so it's lost whenever Caddy's container gets
	// recreated (image rebuild, `docker compose up`, ...) even though
	// existing organizations' headscale/headplane containers keep running
	// -- without this, their OIDC login breaks until the alias is restored.
	if cfg.BaseDomain != "" {
		if err := ctl.EnsureCaddyNetworkAlias(ctx, cfg.BaseDomain); err != nil {
			logger.Warn("ensure caddy network alias", "error", err)
		}
	}

	queries := db.New(pool, logger)
	auth.Setup(cfg.Auth.SessionSecret)
	provider, oidcStorage, err := oidcprovider.New(cfg, queries.Queries)
	if err != nil {
		return fmt.Errorf("oidc provider: %w", err)
	}
	h := handlers.New(queries, ctl, cfg.BaseDomain, oidcStorage, logger)

	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	mux := server.NewMux(h, requireAuthAPI, logger, webui.Handler(), provider)

	srv := &http.Server{Addr: cfg.Addr, Handler: mux}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr)
		serveErr <- srv.ListenAndServe()
	}()

	// Registering the upstream OIDC provider happens *after* the HTTP
	// server starts listening, and in the background with retries, rather
	// than synchronously here: when the upstream issuer is itself proxied
	// through Caddy under on-demand TLS (e.g. the bundled local-dev Dex
	// instance), Caddy's certificate issuance for that issuer calls back
	// into *this* server's /api/tls-ask endpoint (see server.NewMux) to
	// authorize it -- so discovery against such an issuer can only
	// succeed once this server is already accepting connections. Doing it
	// synchronously before ListenAndServe would deadlock on first boot.
	go registerUpstreamOIDCWithRetry(ctx, logger, cfg)

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	case <-ctx.Done():
		stop()
		logger.Info("shutting down (signal received), waiting for in-flight requests...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
			if closeErr := srv.Close(); closeErr != nil {
				logger.Error("close failed", "error", closeErr)
			}
		}
		logger.Info("shutdown complete")
	}
	return nil
}

// registerUpstreamOIDCRetryInterval is how long registerUpstreamOIDCWithRetry
// waits between failed attempts to register the upstream OIDC provider.
const registerUpstreamOIDCRetryInterval = 2 * time.Second

// registerUpstreamOIDCWithRetry calls auth.RegisterUpstreamOIDC in a retry
// loop until it succeeds or ctx is done, logging each failure. Retrying is
// needed (rather than a single attempt) because the upstream issuer may
// itself depend on this same server already being up and reachable to
// finish its own startup -- see the comment at this function's call site in
// run() -- so a single early failure doesn't necessarily mean
// misconfiguration, just "not ready yet".
func registerUpstreamOIDCWithRetry(ctx context.Context, logger *slog.Logger, cfg *config.Config) {
	// extraCACertPath is only non-empty (and only needs to exist) when
	// Caddy issues its own self-signed local certs (see CaddyLocalCerts
	// above), i.e. local dev with a non-public BASE_DOMAIN -- e.g. Dex
	// proxied through Caddy. In production, RegisterUpstreamOIDC ignores a
	// missing file and uses the system trust store, since Issuer there has
	// a publicly-trusted cert.
	var extraCACertPath string
	if cfg.CaddyLocalCerts != "" {
		extraCACertPath = filepath.Join(dockerctl.CaddyDataMountTarget, dockerctl.CaddyLocalCARelPath)
	}

	for {
		err := auth.RegisterUpstreamOIDC(cfg.Auth.UpstreamOIDC.Issuer, cfg.Auth.UpstreamOIDC.ClientID, cfg.Auth.UpstreamOIDC.ClientSecret, cfg.Auth.UpstreamOIDC.CallbackURL, extraCACertPath)
		if err == nil {
			logger.Info("registered upstream OIDC provider", "issuer", cfg.Auth.UpstreamOIDC.Issuer)
			return
		}
		logger.Warn("register upstream OIDC provider, retrying", "error", err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(registerUpstreamOIDCRetryInterval):
		}
	}
}
