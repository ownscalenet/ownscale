package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/auth/oidcprovider"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/headscaleclient"
	"github.com/ownscalenet/ownscale/orgs"
)

type Handlers struct {
	queries     *db.TxQueries
	orgs        *orgs.Service     // nil if organization provisioning isn't configured (no ctl/base_domain)
	provisioner *orgs.Provisioner // nil under the same conditions as orgs
	hs          machinesRegistry  // nil under the same conditions as orgs
	baseDomain  string
	oidc        *oidcprovider.Storage
	logger      *slog.Logger
}

// machinesRegistry is the narrow interface machines.go needs from a
// *headscaleclient.Registry -- letting tests substitute a fake that
// returns a client pointed at an httptest.Server instead of a real
// hs-<subdomain>:8080 Docker container.
type machinesRegistry interface {
	Client(ctx context.Context, subdomain string) (*headscaleclient.Client, error)
}

// New builds the Handlers. queries serves reads/writes outside the
// organization-provisioning transaction, and (if ctl is non-nil and
// baseDomain is set) backs the orgs.Service used for organization creation
// and the orgs.Provisioner that runs provisioning in the background;
// otherwise provisioning-related handlers respond with 503 (see
// CreateOrganization). It also backs a headscaleclient.Registry (see
// machines.go), used to reach each organization's headscale container; a
// registry backed by a nil ctl reports "not configured" instead of erroring.
// derpCfg is forwarded to orgs.NewService (see its doc comment); its zero
// value is a no-op.
func New(queries *db.TxQueries, ctl *dockerctl.Ctl, baseDomain string, oidc *oidcprovider.Storage, logger *slog.Logger, derpCfg config.DerpConfig) *Handlers {
	h := &Handlers{
		queries:    queries,
		baseDomain: baseDomain,
		oidc:       oidc,
		logger:     logger,
	}
	if ctl != nil && baseDomain != "" {
		issuer := ""
		if oidc != nil {
			issuer = oidc.Issuer()
		}
		h.orgs = orgs.NewService(queries, ctl, baseDomain, issuer, derpCfg)
		h.provisioner = orgs.NewProvisioner(h.orgs, logger, 0)
		h.hs = headscaleclient.NewRegistry(ctl)
	}
	return h
}

// StartProvisioner launches the background organization provisioner (see
// orgs.Provisioner) in its own goroutine, if organization provisioning is
// configured (ctl/base_domain set -- see New). It returns immediately; the
// goroutine runs until ctx is canceled. It's a no-op if provisioning isn't
// configured.
func (h *Handlers) StartProvisioner(ctx context.Context) {
	if h.provisioner == nil {
		return
	}
	go func() {
		if err := h.provisioner.Run(ctx); err != nil {
			h.logger.Error("organization provisioner stopped", "error", err)
		}
	}()
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

// Session returns the currently authenticated user, plus their
// organization (including its provisioning status), as JSON. It is mounted
// behind auth.RequireAuthAPI, so reaching this handler implies a user is
// present in the request context. "organization" is null for a user who
// hasn't created one yet; otherwise the SPA polls this endpoint after
// organization creation until status becomes "active" (see
// CreateOrganization/orgs.Provisioner).
func (h *Handlers) Session(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	var org *sqlcgen.Organization
	if user.OrganizationID.Valid {
		o, err := h.queries.GetOrganization(r.Context(), user.OrganizationID.String)
		if err != nil {
			h.serverError(w, err)
			return
		}
		org = &o
		h.ensureCallerHeadscaleUser(r, user, o)
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"user": user, "organization": org})
}

func (h *Handlers) ensureCallerHeadscaleUser(r *http.Request, user *sqlcgen.User, org sqlcgen.Organization) {
	if user.HeadscaleUserID.Valid || org.Status != "active" {
		return
	}
	client, notConfigured, err := h.resolveMachinesClient(r)
	if err != nil || notConfigured {
		return
	}
	members, err := h.queries.ListUsersByOrganization(r.Context(), user.OrganizationID)
	if err != nil {
		h.logger.Warn("list organization members for headscale user linking", "error", err)
		return
	}
	if _, err := h.ensureHeadscaleUser(r.Context(), client, *user, claimedHeadscaleUserIDs(members)); err != nil {
		h.logger.Warn("ensure headscale user", "user_id", user.ID, "error", err)
	}
}
