package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ownscalenet/ownscale/internal/dockerctl"
	"github.com/ownscalenet/ownscale/internal/orgs"
)

// createOrganizationRequest is the POST /api/organizations request body.
type createOrganizationRequest struct {
	Subdomain string `json:"subdomain"`
}

// CreateOrganization provisions a new organization (headscale + headplane
// containers on subdomain.<base_domain>) and returns the created
// organization record as JSON. This is the HTTP equivalent of the
// `ownscale create-organization <subdomain>` CLI command.
func (h *Handlers) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	if h.ctl == nil || h.baseDomain == "" {
		h.writeError(w, http.StatusServiceUnavailable, "organization provisioning is not configured (base_domain/Docker unavailable)")
		return
	}

	var req createOrganizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	subdomain := strings.TrimSpace(req.Subdomain)
	if err := dockerctl.ValidateSubdomain(subdomain); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	org, err := orgs.Create(r.Context(), h.pool, h.ctl, h.baseDomain, subdomain, h.logger)
	if err != nil {
		h.serverError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{"organization": org})
}
