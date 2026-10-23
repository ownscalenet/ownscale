package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
)

// Roles a user can hold within their organization. Owners and admins
// (managers) can change everything; every other role acts like
// RoleMember, which can only manage its own machines and pre-auth keys.
const (
	RoleOwner        = "owner"
	RoleAdmin        = "admin"
	RoleNetworkAdmin = "network_admin"
	RoleITAdmin      = "it_admin"
	RoleAuditor      = "auditor"
	RoleMember       = "member"
)

// validRoles is the set of roles accepted by requests that set a role
// (invites, member-role changes).
var validRoles = map[string]bool{
	RoleOwner:        true,
	RoleAdmin:        true,
	RoleNetworkAdmin: true,
	RoleITAdmin:      true,
	RoleAuditor:      true,
	RoleMember:       true,
}

// isManager reports whether role can manage members and invites
// (everything except transferring ownership, which is owner-only).
func isManager(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}

// ownsHeadscaleUser reports whether caller is linked to the headscale user
// hsUserID.
func ownsHeadscaleUser(caller *sqlcgen.User, hsUserID string) bool {
	return caller.HeadscaleUserID == hsUserID
}

// assignableUser is a member of the caller's organization that the caller
// may register machines and create pre-auth keys for. ID is the member's
// ownscale user ID.
type assignableUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// assignableUsers returns the members of caller's organization that caller
// may register machines and create pre-auth keys for: all of them for
// managers, otherwise only caller. They come from ownscale, so they're
// known before the organization's headscale first starts.
func (h *Handlers) assignableUsers(ctx context.Context, caller *sqlcgen.User) ([]assignableUser, error) {
	if caller == nil {
		return []assignableUser{}, nil
	}
	members, err := h.queries.ListUsersByOrganization(ctx, caller.OrganizationID)
	if err != nil {
		return nil, err
	}
	out := make([]assignableUser, 0, len(members))
	for _, m := range members {
		if canAssignTo(caller, m.ID) {
			out = append(out, assignableUser{ID: m.ID, Name: m.Name, Email: m.Email})
		}
	}
	return out, nil
}

// canAssignTo reports whether caller may register machines and create
// pre-auth keys for the member memberID.
func canAssignTo(caller *sqlcgen.User, memberID string) bool {
	return caller != nil && (isManager(caller.Role) || caller.ID == memberID)
}

// requireManager writes a 403 with msg and returns false unless the caller
// is an owner or admin.
func (h *Handlers) requireManager(w http.ResponseWriter, r *http.Request, msg string) bool {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return false
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, msg)
		return false
	}
	return true
}

// requireNodeAccess writes a 403 and returns false unless the caller is a
// manager or owns node nodeID. Members may only act on their own machines.
func (h *Handlers) requireNodeAccess(w http.ResponseWriter, r *http.Request, client *hscl.Client, nodeID string) bool {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return false
	}
	if isManager(caller.Role) {
		return true
	}
	node, err := client.GetNode(r.Context(), nodeID)
	if err != nil && !errors.Is(err, hscl.ErrNotFound) {
		h.serverError(w, r, err)
		return false
	}
	if err == nil && node.User != nil && ownsHeadscaleUser(caller, node.User.ID) {
		return true
	}
	h.writeError(w, http.StatusForbidden, "you can only manage your own machines")
	return false
}
