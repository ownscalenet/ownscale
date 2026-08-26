// Package logging builds the shared *slog.Logger used across the admin
// panel's binaries (cmd/ownscale, cmd/migrate) from config.LogConfig.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ownscalenet/ownscale/internal/config"
	"github.com/ownscalenet/ownscale/internal/derrors"
)

func New(cfg config.LogConfig) (_ *slog.Logger, err error) {
	defer derrors.Wrap(&err, "logging.New(%+v)", cfg)

	level := slog.LevelDebug
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "", "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		return nil, fmt.Errorf("unknown log format %q (want %q or %q)", cfg.Format, "json", "text")
	}

	return slog.New(handler), nil
}
