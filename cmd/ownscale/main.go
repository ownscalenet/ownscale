package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/auth/oidcprovider"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/debugserver"
	"github.com/ownscalenet/ownscale/handlers"
	"github.com/ownscalenet/ownscale/logging"
	"github.com/ownscalenet/ownscale/server"
	"github.com/ownscalenet/ownscale/webui"
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
// os.Exit) on failure so every deferred cleanup always runs.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	logger, err := logging.New(cfg.Log)
	if err != nil {
		return err
	}

	pool, err := db.Connect(cfg.Database())
	if err != nil {
		return err
	}
	defer pool.Close()

	ctx := context.Background()
	queries := db.New(pool, logger)

	auth.Setup(cfg.Auth.SessionSecret)
	provider, oidcStorage, err := oidcprovider.New(cfg, queries.Queries)
	if err != nil {
		return err
	}
	h, err := handlers.New(queries, cfg.BaseDomain, cfg.DataDir, oidcStorage, logger, cfg.Derp, cfg.Auth, cfg.Funnel)
	if err != nil {
		return err
	}
	defer h.Close()

	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	consoleMux := server.NewMux(h, requireAuthAPI, logger, webui.Handler(cfg.Development), provider)
	handler := server.NewSubdomainProxy(consoleMux, cfg.BaseDomain, h.DialOrg, logger)

	// No Read/WriteTimeout: headscale long-polls and funnel WebSockets stay open.
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if cfg.DebugAddr != "" {
		dln, err := net.Listen("tcp", cfg.DebugAddr)
		if err != nil {
			return fmt.Errorf("listen on %q: %w", cfg.DebugAddr, err)
		}
		defer dln.Close()
		go func() {
			logger.Info("debug server listening", "addr", cfg.DebugAddr)
			_ = http.Serve(dln, debugserver.Handler())
		}()
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	h.Start(ctx)

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

	if cfg.Auth.UpstreamOIDC.Enabled() {
		if err := auth.RegisterUpstreamOIDC(
			cfg.Auth.UpstreamOIDC.Issuer,
			cfg.Auth.UpstreamOIDC.ClientID,
			cfg.Auth.UpstreamOIDC.ClientSecret,
			cfg.Auth.UpstreamOIDC.CallbackURL); err != nil {
			return err
		}
	}
	if cfg.Auth.Google.Enabled() {
		if err := auth.RegisterGoogle(
			cfg.Auth.Google.ClientID,
			cfg.Auth.Google.ClientSecret,
			cfg.Auth.Google.CallbackURL); err != nil {
			return err
		}
	}
	if cfg.Auth.Microsoft.Enabled() {
		if err := auth.RegisterMicrosoft(
			cfg.Auth.Microsoft.ClientID,
			cfg.Auth.Microsoft.ClientSecret,
			cfg.Auth.Microsoft.CallbackURL); err != nil {
			return err
		}
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
