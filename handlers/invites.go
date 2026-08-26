package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
)

// inviteTokenTTL is how long a created invite remains acceptable.
const inviteTokenTTL = 7 * 24 * time.Hour

// invite is the JSON shape returned for a pending invite.
type invite struct {
	ID        string  `json:"id"`
	Email     *string `json:"email"`
	Role      string  `json:"role"`
	Token     string  `json:"token"`
	CreatedAt string  `json:"createdAt"`
	ExpiresAt string  `json:"expiresAt"`
}

func toInvite(i sqlcgen.Invite) invite {
	v := invite{
		ID:        i.ID,
		Role:      i.Role,
		Token:     i.Token,
		CreatedAt: i.CreatedAt.Format(time.RFC3339),
		ExpiresAt: i.ExpiresAt.Format(time.RFC3339),
	}
	if i.Email.Valid {
		v.Email = &i.Email.String
	}
	return v
}

// randomToken returns a random URL-safe hex token, suitable for an invite
// link (/invite/{token}).
func randomToken() (_ string, err error) {
	defer derrors.Wrap(&err, "handlers.randomToken()")

	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// CreateInvite handles POST /api/invites. Admin/owner only. There's no
// email infrastructure in ownscale today, so this doesn't send anything --
// it just returns the invite (including its token) for the caller to share
// the /invite/{token} link out-of-band.
func (h *Handlers) CreateInvite(w http.ResponseWriter, r *http.Request) {
	caller, orgID, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can invite members")
		return
	}
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = RoleMember
	}
	if req.Role == RoleOwner || !validRoles[req.Role] {
		h.writeError(w, http.StatusBadRequest, "invalid role")
		return
	}

	token, err := randomToken()
	if err != nil {
		h.serverError(w, err)
		return
	}

	email := sql.NullString{}
	if req.Email != "" {
		email = sql.NullString{String: req.Email, Valid: true}
	}

	inv, err := h.queries.CreateInvite(r.Context(), sqlcgen.CreateInviteParams{
		ID:             uuid.Must(uuid.NewV7()).String(),
		OrganizationID: orgID,
		Email:          email,
		Role:           req.Role,
		Token:          token,
		CreatedBy:      caller.ID,
		ExpiresAt:      time.Now().Add(inviteTokenTTL),
	})
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, map[string]any{"invite": toInvite(inv)})
}

// ListInvites handles GET /api/invites. Admin/owner only; returns every
// pending (not yet accepted) invite for the caller's organization.
func (h *Handlers) ListInvites(w http.ResponseWriter, r *http.Request) {
	caller, orgID, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can view invites")
		return
	}
	invites, err := h.queries.ListInvitesByOrganization(r.Context(), orgID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	out := make([]invite, 0, len(invites))
	for _, i := range invites {
		out = append(out, toInvite(i))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"invites": out})
}

// DeleteInvite handles DELETE /api/invites/{id}. Admin/owner only; revokes
// a pending invite (accepted or not) so its token stops working.
func (h *Handlers) DeleteInvite(w http.ResponseWriter, r *http.Request) {
	caller, orgID, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can revoke invites")
		return
	}
	id := r.PathValue("id")
	inv, err := h.queries.GetInvite(r.Context(), id)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "invite not found")
		return
	}
	if inv.OrganizationID != orgID {
		h.writeError(w, http.StatusNotFound, "invite not found")
		return
	}
	if err := h.queries.DeleteInvite(r.Context(), id); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GetInvitePreview handles GET /api/invites/{token}. It's public
// (unauthenticated) so the /invite/{token} accept page can show which
// organization the invite is for, and whether it's still valid, before the
// visitor logs in.
func (h *Handlers) GetInvitePreview(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	inv, err := h.queries.GetInviteByToken(r.Context(), token)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "invite not found")
		return
	}
	org, err := h.queries.GetOrganization(r.Context(), inv.OrganizationID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	valid := !inv.AcceptedAt.Valid && inv.ExpiresAt.After(time.Now())
	h.writeJSON(w, http.StatusOK, map[string]any{
		"valid":            valid,
		"organizationName": org.Subdomain,
		"role":             inv.Role,
	})
}

// AcceptInvite handles POST /api/invites/{token}/accept. It requires an
// authenticated caller who doesn't already belong to an organization (the
// same one-org-per-user constraint CreateOrganization enforces); on
// success it attaches the caller to the invite's organization with the
// invite's role and marks the invite accepted.
func (h *Handlers) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if user.OrganizationID.Valid {
		h.writeError(w, http.StatusConflict, "user already belongs to an organization")
		return
	}

	token := r.PathValue("token")
	inv, err := h.queries.GetInviteByToken(r.Context(), token)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "invite not found")
		return
	}
	if inv.AcceptedAt.Valid {
		h.writeError(w, http.StatusGone, "invite has already been used")
		return
	}
	if inv.ExpiresAt.Before(time.Now()) {
		h.writeError(w, http.StatusGone, "invite has expired")
		return
	}

	if err := h.queries.SetUserOrganizationAndRole(r.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
		OrganizationID: sql.NullString{String: inv.OrganizationID, Valid: true},
		Role:           inv.Role,
		ID:             user.ID,
	}); err != nil {
		h.serverError(w, err)
		return
	}
	if err := h.queries.MarkInviteAccepted(r.Context(), sqlcgen.MarkInviteAcceptedParams{
		AcceptedByUserID: sql.NullString{String: user.ID, Valid: true},
		ID:               inv.ID,
	}); err != nil {
		h.serverError(w, err)
		return
	}

	org, err := h.queries.GetOrganization(r.Context(), inv.OrganizationID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"organization": org})
}
