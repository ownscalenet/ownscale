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
	"github.com/ownscalenet/ownscale/backup"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/debugserver"
	"github.com/ownscalenet/ownscale/handlers"
	"github.com/ownscalenet/ownscale/logging"
	"github.com/ownscalenet/ownscale/orgs"
	"github.com/ownscalenet/ownscale/server"
	"github.com/ownscalenet/ownscale/tlsask"
	"github.com/ownscalenet/ownscale/webui"
)

const (
	// shutdownTimeout bounds how long we wait for in-flight requests to finish
	// before forcing the process to exit.
	shutdownTimeout = 30 * time.Second
	// orgsCloseTimeout bounds stopping every organization's headscale.
	orgsCloseTimeout = 30 * time.Second
	// backupCloseTimeout bounds the final sync to the backup replica.
	backupCloseTimeout = 30 * time.Second
)

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

	ctx := context.Background()

	unlock, err := orgs.LockDataDir(cfg.DataDir)
	if err != nil {
		return err
	}
	defer unlock()

	// Litestream must outlive every connection to the databases it
	// replicates, so it's closed last, and not at all while a headscale
	// failed to stop: its changes stay in the WAL and sync on next start.
	orgsStopped := true
	backups, err := backup.New(ctx, cfg.Backup, cfg.DataDir, logger)
	if err != nil {
		return err
	}
	defer func() {
		if !orgsStopped {
			logger.Error("skipping final backup sync, some organizations didn't stop")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), backupCloseTimeout)
		defer cancel()
		if err := backups.Close(ctx); err != nil {
			logger.Error("close backups", "error", err)
		}
	}()

	pool, err := db.Connect(cfg.Database())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := backups.Add(config.DatabaseFile); err != nil {
		return err
	}

	queries := db.New(pool, logger)

	auth.Setup(cfg.Auth.SessionSecret)
	provider, oidcStorage, err := oidcprovider.New(cfg, queries.Queries)
	if err != nil {
		return err
	}
	h, err := handlers.New(queries, cfg, oidcStorage, logger, backups)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), orgsCloseTimeout)
		defer cancel()
		if err := h.Close(ctx); err != nil {
			orgsStopped = false
			logger.Error("stop organizations", "error", err)
		}
	}()

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

	if cfg.TLSAskAddr != "" {
		aln, err := net.Listen("tcp", cfg.TLSAskAddr)
		if err != nil {
			return fmt.Errorf("listen on %q: %w", cfg.TLSAskAddr, err)
		}
		defer aln.Close()
		askSrv := &http.Server{
			Handler:           tlsask.Handler(queries.Queries, cfg.BaseDomain, cfg.ContentDomain, logger),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			logger.Info("tls ask server listening", "addr", cfg.TLSAskAddr)
			_ = askSrv.Serve(aln)
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
	if cfg.Auth.GitHub.Enabled() {
		if err := auth.RegisterGitHub(
			cfg.Auth.GitHub.ClientID,
			cfg.Auth.GitHub.ClientSecret,
			cfg.Auth.GitHub.CallbackURL); err != nil {
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
