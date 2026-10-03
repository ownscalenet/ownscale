package handlers

import (
	"net/http"
	"slices"

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
	return caller.HeadscaleUserID.Valid && caller.HeadscaleUserID.String == hsUserID
}

// assignableUsers returns the headscale users caller may register machines
// and create pre-auth keys for: all of them for managers, otherwise only the
// one caller is linked to.
func assignableUsers(caller *sqlcgen.User, users []hscl.User) []hscl.User {
	if caller != nil && isManager(caller.Role) {
		return users
	}
	return slices.DeleteFunc(slices.Clone(users), func(u hscl.User) bool {
		return caller == nil || !ownsHeadscaleUser(caller, u.ID)
	})
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
	nodes, err := client.ListNodes(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return false
	}
	for _, n := range nodes {
		if n.ID == nodeID && n.User != nil && ownsHeadscaleUser(caller, n.User.ID) {
			return true
		}
	}
	h.writeError(w, http.StatusForbidden, "you can only manage your own machines")
	return false
}
