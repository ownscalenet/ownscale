package handlers

import (
	"database/sql"
	"net/http"
	"strings"

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
// organization ID, writing a 401/409 response (and returning ok=false) if
// the caller isn't authenticated or has no organization.
func (h *Handlers) callerAndOrg(w http.ResponseWriter, r *http.Request) (caller *sqlcgen.User, orgID string, ok bool) {
	user, authed := auth.UserFromContext(r.Context())
	if !authed {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return nil, "", false
	}
	if !user.OrganizationID.Valid {
		h.writeError(w, http.StatusConflict, "user does not belong to an organization")
		return nil, "", false
	}
	return user, user.OrganizationID.String, true
}

// toMember converts u into its JSON representation.
func toMember(u sqlcgen.User) member {
	return member{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role}
}

// ListMembers handles GET /api/members. It returns every member of the
// caller's organization, plus whether the caller can manage members
// (owner/admin). Members aren't linked to a headscale user by hand: any
// member who isn't linked yet is automatically matched, by email, against
// the organization's headscale users (populated by headscale's own OIDC
// login when someone runs `tailscale up`) and the link is persisted --
// best-effort, since headscale may not be reachable yet or no match may
// exist. The link itself stays purely an internal bookkeeping detail (see
// the member struct) so headscale users are never surfaced to end-users.
func (h *Handlers) ListMembers(w http.ResponseWriter, r *http.Request) {
	caller, orgID, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}

	users, err := h.queries.ListUsersByOrganization(r.Context(), sql.NullString{String: orgID, Valid: true})
	if err != nil {
		h.serverError(w, err)
		return
	}

	if client, notConfigured, err := h.resolveMachinesClient(r); err == nil && !notConfigured {
		if hsUsers, err := client.ListUsers(r.Context()); err == nil {
			byEmail := map[string]string{}
			for _, u := range hsUsers {
				if u.Email != "" {
					byEmail[strings.ToLower(u.Email)] = u.ID
				}
			}
			for i, u := range users {
				if u.HeadscaleUserID.Valid || u.Email == "" {
					continue
				}
				hsID, matched := byEmail[strings.ToLower(u.Email)]
				if !matched {
					continue
				}
				if err := h.queries.LinkUserHeadscaleUser(r.Context(), sqlcgen.LinkUserHeadscaleUserParams{
					HeadscaleUserID: sql.NullString{String: hsID, Valid: true},
					ID:              u.ID,
				}); err != nil {
					// Best-effort: e.g. a unique-constraint conflict if
					// another member is already linked to this headscale
					// user. Leave this member unlinked and move on.
					continue
				}
				users[i].HeadscaleUserID = sql.NullString{String: hsID, Valid: true}
			}
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
	if !target.OrganizationID.Valid || target.OrganizationID.String != orgID {
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
	caller, orgID, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can change member roles")
		return
	}
	target, ok := h.targetMember(w, r, orgID)
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
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// RemoveMember handles DELETE /api/members/{id}. Only an admin/owner may
// remove a member, and the owner can't be removed this way -- ownership
// must be transferred first (see TransferOwnership), which both protects
// against ever leaving an organization without an owner and matches
// headplane's behavior. Removing a member only clears their organization
// membership (role/link); it does not delete their ownscale account, which
// is recreated on their next login should they be re-invited.
func (h *Handlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
	caller, orgID, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can remove members")
		return
	}
	target, ok := h.targetMember(w, r, orgID)
	if !ok {
		return
	}
	if target.Role == RoleOwner {
		h.writeError(w, http.StatusBadRequest, "transfer ownership before removing the owner")
		return
	}
	if err := h.queries.RemoveUserFromOrganization(r.Context(), target.ID); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// TransferOwnership handles POST /api/members/transfer-ownership. Only the
// current owner may call it; it promotes the given member to owner and
// demotes the caller to admin, so there's always exactly one owner.
func (h *Handlers) TransferOwnership(w http.ResponseWriter, r *http.Request) {
	caller, orgID, ok := h.callerAndOrg(w, r)
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
	target, ok := h.targetMember(w, r, orgID)
	if !ok {
		return
	}
	if target.ID == caller.ID {
		h.writeError(w, http.StatusBadRequest, "you already own this organization")
		return
	}
	if err := h.queries.UpdateUserRole(r.Context(), sqlcgen.UpdateUserRoleParams{Role: RoleOwner, ID: target.ID}); err != nil {
		h.serverError(w, err)
		return
	}
	if err := h.queries.UpdateUserRole(r.Context(), sqlcgen.UpdateUserRoleParams{Role: RoleAdmin, ID: caller.ID}); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
