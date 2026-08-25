package handlers

// Roles a user can hold within their organization, mirroring headplane's
// role set for UI/data parity. Only RoleOwner and RoleAdmin are actually
// enforced anywhere in ownscale today (see members.go/headscale_users.go/
// invites.go) -- the others are stored and displayed for parity but don't
// yet gate any ownscale-specific feature, since ownscale has no equivalent
// to headplane's ACL/DNS/etc. admin surfaces to gate against.
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

// isManager reports whether role can manage members, headscale users, and
// invites (everything except transferring ownership, which is owner-only).
func isManager(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}
