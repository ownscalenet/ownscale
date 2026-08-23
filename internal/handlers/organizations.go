package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/dockerctl"
)

// createOrganizationRequest is the POST /api/organizations request body.
type createOrganizationRequest struct {
	Subdomain string `json:"subdomain"`
}

// createOrganizationTimeout bounds how long organization provisioning
// (docker container creation, image pulls, network setup) is allowed to
// run once started. It's applied to a context detached from the incoming
// request (see CreateOrganization) so that a client disconnecting or
// reloading mid-request doesn't cancel in-flight Docker operations and
// leave the organization half-provisioned.
const createOrganizationTimeout = 2 * time.Minute

// CreateOrganization provisions a new organization (headscale + headplane
// containers on subdomain.<base_domain>) for the calling user and returns
// the created organization record as JSON. It is mounted behind
// auth.RequireAuthAPI, so reaching this handler implies a user is present
// in the request context; that user is attached to the new organization as
// its owner. Since the current data model supports at most one
// organization per user, a user who already has one gets a 409 Conflict
// instead of a second, orphaned organization.
func (h *Handlers) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	if h.orgs == nil {
		h.writeError(w, http.StatusServiceUnavailable, "organization provisioning is not configured (base_domain/Docker unavailable)")
		return
	}

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

	// Provisioning (Docker container creation, image pulls, network setup)
	// can take longer than a client is willing to wait around for. Detach
	// from the request's cancellation so a disconnecting/reloading client
	// doesn't cancel in-flight Docker operations mid-way and leave the
	// organization half-provisioned; still bound it with a timeout so a
	// truly stuck operation doesn't run forever.
	//
	// Another problem with this request: when the org is created, a new network
	// is instantiated. That even get's picked up by the OS, so firefox/chrome
	// also pick it up. They see something about machine's network changes and
	// they drop all in-progress requests.
	ctx, cancel := context.WithTimeout(context.TODO(), createOrganizationTimeout)
	defer cancel()

	org, err := h.orgs.Create(ctx, subdomain)
	if err != nil {
		h.serverError(w, err)
		return
	}

	if err := h.queries.SetUserOrganization(ctx, sqlcgen.SetUserOrganizationParams{
		OrganizationID: sql.NullString{String: org.ID, Valid: true},
		ID:             user.ID,
	}); err != nil {
		h.serverError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{"organization": org})
}
