package handlers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/derrors"
	"github.com/ownscalenet/ownscale/internal/dockerctl"
)

type Handlers struct {
	pool       *sql.DB
	queries    *sqlcgen.Queries
	ctl        *dockerctl.Ctl
	baseDomain string
	logger     *slog.Logger
}

func New(pool *sql.DB, ctl *dockerctl.Ctl, baseDomain string, logger *slog.Logger) (_ *Handlers, err error) {
	defer derrors.Wrap(&err, "handlers.New(%q)", baseDomain)

	return &Handlers{
		pool:       pool,
		queries:    sqlcgen.New(db.NewLoggingDBTX(pool, logger)),
		ctl:        ctl,
		baseDomain: baseDomain,
		logger:     logger,
	}, nil
}

// writeJSON encodes v as the JSON response body with the given status code.
func (h *Handlers) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.logger.Error("encode json response", "error", err)
	}
}

// writeError writes a JSON error body {"error": msg} with the given status code.
func (h *Handlers) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}

func (h *Handlers) serverError(w http.ResponseWriter, err error) {
	h.logger.Error("request failed", "error", err)
	h.writeError(w, http.StatusInternalServerError, "internal server error")
}

// Queries exposes the sqlc queries for wiring auth middleware in main.
func (h *Handlers) Queries() *sqlcgen.Queries {
	return h.queries
}

// Logger exposes the request logger for wiring logging middleware in server.NewMux.
func (h *Handlers) Logger() *slog.Logger {
	return h.logger
}

// Session returns the currently authenticated user as JSON. It is mounted
// behind auth.RequireAuthAPI, so reaching this handler implies a user is
// present in the request context.
func (h *Handlers) Session(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"user": user})
}
