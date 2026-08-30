package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/dockerctl"
)

// createOrganizationRequest is the POST /api/organizations request body.
type createOrganizationRequest struct {
	Subdomain string `json:"subdomain"`
}

// CreateOrganization inserts a new organization (status "pending") for the
// calling user and returns it as JSON, handing off the actual provisioning
// (headscale + headplane containers on subdomain.<base_domain>) to the
// background provisioner. It is mounted behind auth.RequireAuthAPI, so
// reaching this handler implies a user is present in the request context;
// that user is attached to the new organization as its owner. Since the
// current data model supports at most one organization per user, a user
// who already has one gets a 409 Conflict instead of a second, orphaned
// organization.
func (h *Handlers) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if user.OrganizationID.Valid {
		h.writeError(w, http.StatusConflict, "user already belongs to an organization")
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

	// Only the fast DB insert (status "pending") happens inline here; the
	// actual provisioning (Docker container creation, image pulls, network
	// setup) is handed off to the background provisioner via Signal below.
	// This request returning quickly, before any Docker network gets
	// created, is exactly what avoids the problem described above: a new
	// Docker network is picked up by the host OS as a network change, which
	// makes Firefox (and other browsers) drop in-flight requests --
	// including this one, if it were still running when that happened.
	org, err := h.orgs.CreatePending(r.Context(), subdomain)
	if err != nil {
		h.serverError(w, err)
		return
	}

	if err := h.queries.SetUserOrganizationAndRole(r.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
		OrganizationID: sql.NullString{String: org.ID, Valid: true},
		Role:           RoleOwner,
		ID:             user.ID,
	}); err != nil {
		h.serverError(w, err)
		return
	}

	h.provisioner.Signal(org.ID)

	h.writeJSON(w, http.StatusCreated, map[string]any{"organization": h.toOrganizationResponse(org)})
}
