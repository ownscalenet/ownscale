package handlers

import (
	"database/sql"
	"net/http"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// member is the JSON shape of an org member returned by ListMembers. The
// member's linked headscale user (see ListMembers' auto-linking) is an
// internal implementation detail and deliberately not exposed here --
// headscale users aren't a concept end-users should need to know about.
type member struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// callerAndOrg resolves the authenticated caller's *sqlcgen.User and their
// organization, writing a 401 response (and returning ok=false) if the
// caller isn't authenticated. On success, req is r with its context
// enriched by h.withOrgLogger so downstream logging (including any further
// h.serverError call using req) is annotated with the organization's
// subdomain.
func (h *Handlers) callerAndOrg(w http.ResponseWriter, r *http.Request) (caller *sqlcgen.User, org sqlcgen.Organization, req *http.Request, ok bool) {
	user, authed := auth.UserFromContext(r.Context())
	if !authed {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return nil, sqlcgen.Organization{}, r, false
	}
	o, err := h.queries.GetOrganization(r.Context(), user.OrganizationID)
	if err != nil {
		h.serverError(w, r, err)
		return nil, sqlcgen.Organization{}, r, false
	}
	r = r.WithContext(h.withOrgLogger(r.Context(), o.Subdomain))
	return user, o, r, true
}

// toMember converts u into its JSON representation.
func toMember(u sqlcgen.User) member {
	return member{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role}
}

// ListMembers handles GET /api/members. It returns every member of the
// caller's organization, plus whether the caller can manage members
// (owner/admin).
func (h *Handlers) ListMembers(w http.ResponseWriter, r *http.Request) {
	caller, org, r, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}

	users, err := h.queries.ListUsersByOrganization(r.Context(), org.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	if client, _, notConfigured, err := h.resolveHeadscaleClient(r); err == nil && !notConfigured {
		claimed := claimedHeadscaleUserIDs(users)
		for i, u := range users {
			if u.HeadscaleUserID.Valid {
				continue
			}
			hsID, err := h.ensureHeadscaleUser(r.Context(), client, u, claimed)
			if err != nil {
				// Best-effort: e.g. headscale rejected the create call, or
				// a unique-constraint conflict racing another request.
				// Leave this member unlinked and move on.
				continue
			}
			users[i].HeadscaleUserID = sql.NullString{String: hsID, Valid: true}
			claimed[hsID] = true
		}
	}

	members := make([]member, 0, len(users))
	for _, u := range users {
		members = append(members, toMember(u))
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"members":    members,
		"canManage":  isManager(caller.Role),
		"callerId":   caller.ID,
		"callerRole": caller.Role,
	})
}

// targetMember loads the {id} path member and verifies it belongs to the
// caller's organization, writing a 404 (and returning ok=false) otherwise.
func (h *Handlers) targetMember(w http.ResponseWriter, r *http.Request, orgID string) (target sqlcgen.User, ok bool) {
	id := r.PathValue("id")
	target, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "member not found")
		return sqlcgen.User{}, false
	}
	if target.OrganizationID != orgID {
		h.writeError(w, http.StatusNotFound, "member not found")
		return sqlcgen.User{}, false
	}
	return target, true
}

// UpdateMemberRole handles POST /api/members/{id}/role. Only an admin/owner
// may change another member's role; granting or revoking the owner role
// must go through TransferOwnership instead, so both the target's current
// role and the requested role are rejected here if either is "owner".
func (h *Handlers) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	caller, org, r, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can change member roles")
		return
	}
	target, ok := h.targetMember(w, r, org.ID)
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if target.Role == RoleOwner || req.Role == RoleOwner {
		h.writeError(w, http.StatusBadRequest, "use transfer-ownership to change the owner")
		return
	}
	if !validRoles[req.Role] {
		h.writeError(w, http.StatusBadRequest, "invalid role")
		return
	}
	if err := h.queries.UpdateUserRole(r.Context(), sqlcgen.UpdateUserRoleParams{Role: req.Role, ID: target.ID}); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// RemoveMember handles DELETE /api/members/{id}. Only an admin/owner may
// remove a member, and the owner can't be removed this way -- ownership
// must be transferred first (see TransferOwnership), which protects
// against ever leaving an organization without an owner. Removing a member
// deletes their ownscale account outright (organization_id is NOT NULL, so
// there's no "member with no org" state to leave them in); a genuinely
// fresh account (with its own freshly pool-assigned organization, or the
// invited organization) is created the next time they log in.
func (h *Handlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
	caller, org, r, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can remove members")
		return
	}
	target, ok := h.targetMember(w, r, org.ID)
	if !ok {
		return
	}
	if target.Role == RoleOwner {
		h.writeError(w, http.StatusBadRequest, "transfer ownership before removing the owner")
		return
	}
	if err := h.queries.DeleteUser(r.Context(), target.ID); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// TransferOwnership handles POST /api/members/transfer-ownership. Only the
// current owner may call it; it promotes the given member to owner and
// demotes the caller to admin, so there's always exactly one owner.
func (h *Handlers) TransferOwnership(w http.ResponseWriter, r *http.Request) {
	caller, org, r, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if caller.Role != RoleOwner {
		h.writeError(w, http.StatusForbidden, "only the owner can transfer ownership")
		return
	}
	var req struct {
		MemberID string `json:"memberId"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	r.SetPathValue("id", req.MemberID)
	target, ok := h.targetMember(w, r, org.ID)
	if !ok {
		return
	}
	if target.ID == caller.ID {
		h.writeError(w, http.StatusBadRequest, "you already own this organization")
		return
	}
	if err := h.queries.UpdateUserRole(r.Context(), sqlcgen.UpdateUserRoleParams{Role: RoleOwner, ID: target.ID}); err != nil {
		h.serverError(w, r, err)
		return
	}
	if err := h.queries.UpdateUserRole(r.Context(), sqlcgen.UpdateUserRoleParams{Role: RoleAdmin, ID: caller.ID}); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
