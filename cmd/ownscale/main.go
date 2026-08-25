package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
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

	ctl, err := dockerctl.New(cfg.DataDir, cfg.LocalCADir, logger)
	if err != nil {
		return fmt.Errorf("docker init: %w", err)
	}
	defer ctl.Close()
	ctx := context.Background()
	if err := ctl.StreamAllLogs(ctx); err != nil {
		logger.Warn("reattach organization container logs", "error", err)
	}

	queries := db.New(pool, logger)

	// Re-assert Caddy's network alias for BaseDomain on every startup: it's
	// set imperatively via the Docker API (not declared in
	// docker-compose.yml), so it's lost whenever Caddy's container gets
	// recreated (image rebuild, `docker compose up`, ...). This covers the
	// shared network (used by the ownscale app container itself), plus --
	// since each organization's headscale/headplane containers live only on
	// their own isolated network, not the shared one -- every existing
	// organization's own network individually, so their OIDC login doesn't
	// silently break until the alias is restored.
	if cfg.BaseDomain != "" {
		if err := ctl.EnsureCaddyNetworkAlias(ctx, cfg.BaseDomain); err != nil {
			logger.Warn("ensure caddy network alias", "error", err)
		}
		if organizations, err := queries.ListOrganizations(ctx); err != nil {
			logger.Warn("list organizations for caddy network alias reconciliation", "error", err)
		} else {
			for _, org := range organizations {
				if err := ctl.EnsureCaddyNetworkAliasForOrg(ctx, org.Subdomain, cfg.BaseDomain); err != nil {
					logger.Warn("ensure caddy network alias for organization", "subdomain", org.Subdomain, "error", err)
				}
				// Likewise, ownscale's own membership in each organization's
				// network (needed to reach hs-<subdomain>:8080 directly for
				// the Machines tab) is set imperatively too, so it must be
				// reconciled the same way.
				if err := ctl.EnsureOwnscaleNetworkForOrg(ctx, org.Subdomain); err != nil {
					logger.Warn("ensure ownscale network join for organization", "subdomain", org.Subdomain, "error", err)
				}
			}
		}
	}

	auth.Setup(cfg.Auth.SessionSecret)
	provider, oidcStorage, err := oidcprovider.New(cfg, queries.Queries)
	if err != nil {
		return fmt.Errorf("oidc provider: %w", err)
	}
	h := handlers.New(queries, ctl, cfg.BaseDomain, oidcStorage, logger, cfg.Derp)

	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	mux := server.NewMux(h, requireAuthAPI, logger, webui.Handler(), provider)

	srv := &http.Server{Addr: cfg.Addr, Handler: mux}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	h.StartProvisioner(ctx)

	// Listen before RegisterUpstreamOIDC because that call needs the server to be up.
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", cfg.Addr, err)
	}
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr)
		serveErr <- srv.Serve(ln)
	}()

	if err := auth.RegisterUpstreamOIDC(cfg.Auth.UpstreamOIDC.Issuer, cfg.Auth.UpstreamOIDC.ClientID, cfg.Auth.UpstreamOIDC.ClientSecret, cfg.Auth.UpstreamOIDC.CallbackURL); err != nil {
		return fmt.Errorf("register upstream OIDC provider: %w", err)
	}

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
