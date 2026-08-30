package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

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
	orgs        *orgs.Service
	provisioner *orgs.Provisioner
	hs          *headscaleclient.Registry
	baseDomain  string
	oidc        *oidcprovider.Storage
	logger      *slog.Logger
}

// New builds the Handlers, wiring queries/ctl/baseDomain into the
// orgs.Service used for organization creation and the orgs.Provisioner
// that runs provisioning in the background.
func New(queries *db.TxQueries, ctl *dockerctl.Ctl, baseDomain string, oidc *oidcprovider.Storage, logger *slog.Logger, derpCfg config.DerpConfig) *Handlers {
	issuer := ""
	if oidc != nil {
		issuer = oidc.Issuer()
	}
	orgSvc := orgs.NewService(queries, ctl, baseDomain, issuer, derpCfg)
	return &Handlers{
		queries:     queries,
		baseDomain:  baseDomain,
		oidc:        oidc,
		logger:      logger,
		orgs:        orgSvc,
		provisioner: orgs.NewProvisioner(orgSvc, logger, 0),
		hs:          headscaleclient.NewRegistry(ctl),
	}
}

// StartProvisioner launches the background organization provisioner (see
// orgs.Provisioner) in its own goroutine. It returns immediately; the
// goroutine runs until ctx is canceled.
func (h *Handlers) StartProvisioner(ctx context.Context) {
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

type organizationResponse struct {
	Domain    string    `json:"Domain"`
	CreatedAt time.Time `json:"CreatedAt"`
	Status    string    `json:"Status"`
}

func (h *Handlers) toOrganizationResponse(org sqlcgen.Organization) organizationResponse {
	return organizationResponse{
		Domain:    org.Subdomain + "." + h.baseDomain,
		CreatedAt: org.CreatedAt,
		Status:    org.Status,
	}
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

	var orgResp any
	if org != nil {
		orgResp = h.toOrganizationResponse(*org)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"user": user, "organization": orgResp})
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
