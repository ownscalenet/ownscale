package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/config"
	"github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/dockerctl"
	"github.com/ownscalenet/ownscale/internal/handlers"
	"github.com/ownscalenet/ownscale/internal/logging"
	"github.com/ownscalenet/ownscale/internal/server"
	"github.com/ownscalenet/ownscale/internal/webui"
)

// shutdownTimeout bounds how long we wait for in-flight requests to finish
// forcing the process to exit.
const shutdownTimeout = 10 * time.Second

func main() {
	// bootstrap is a minimal default logger used only until the real one
	// (built from the loaded config's Log settings) is available.
	bootstrap := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config file (default: "+config.DefaultPath+")")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		bootstrap.Error("load config", "error", err)
		os.Exit(1)
	}

	logger, err := logging.New(cfg.Log)
	if err != nil {
		bootstrap.Error("build logger", "error", err)
		os.Exit(1)
	}

	pool, err := db.Connect(cfg.Database())
	if err != nil {
		logger.Error("db connect", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := cfg.Validate(); err != nil {
		logger.Error("validate config", "error", err)
		os.Exit(1)
	}

	ctl, err := dockerctl.New(cfg.DataDir)
	if err != nil {
		logger.Error("docker init", "error", err)
		os.Exit(1)
	}
	defer ctl.Close()

	h, err := handlers.New(pool, ctl, cfg.BaseDomain, logger)
	if err != nil {
		logger.Error("handlers init", "error", err)
		os.Exit(1)
	}

	auth.Setup(cfg.Auth.SessionSecret, cfg.Auth.Google.ClientID, cfg.Auth.Google.ClientSecret, cfg.Auth.Google.CallbackURL)

	mux := server.NewMux(h, webui.Handler())

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
			logger.Error("serve", "error", err)
			os.Exit(1)
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
}
