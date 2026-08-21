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
	"syscall"
	"time"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/config"
	"github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
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

	ctl, err := dockerctl.New(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("docker init: %w", err)
	}
	defer ctl.Close()

	queries := sqlcgen.New(db.NewLoggingDBTX(pool, logger))
	h := handlers.New(queries, pool, ctl, cfg.BaseDomain, logger)

	auth.Setup(cfg.Auth.SessionSecret, cfg.Auth.Google.ClientID, cfg.Auth.Google.ClientSecret, cfg.Auth.Google.CallbackURL)

	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries, next)
	}
	mux := server.NewMux(h, requireAuthAPI, logger, webui.Handler())

	srv := &http.Server{Addr: cfg.Addr, Handler: mux}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr)
		serveErr <- srv.ListenAndServe()
	}()

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
