package handlers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/dockerctl"
	"github.com/ownscalenet/ownscale/internal/orgs"
)

type Handlers struct {
	queries    *sqlcgen.Queries
	orgs       *orgs.Service // nil if organization provisioning isn't configured (no ctl/base_domain)
	baseDomain string
	logger     *slog.Logger
}

// New builds the Handlers. queries serves reads/writes outside the
// organization-provisioning transaction; pool and ctl are used to build the
// orgs.Service that backs organization creation, and are only required if
// ctl is non-nil and baseDomain is set (otherwise provisioning-related
// handlers respond with 503, see CreateOrganization).
func New(queries *sqlcgen.Queries, pool *sql.DB, ctl *dockerctl.Ctl, baseDomain string, logger *slog.Logger) *Handlers {
	h := &Handlers{
		queries:    queries,
		baseDomain: baseDomain,
		logger:     logger,
	}
	if ctl != nil && baseDomain != "" {
		h.orgs = orgs.NewService(pool, ctl, baseDomain, logger)
	}
	return h
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
