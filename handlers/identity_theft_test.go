package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
)

// nodesOf returns the names of the machines owned by headscale user
// headscaleUserID.
func nodesOf(t *testing.T, client *hscl.Client, headscaleUserID string) []string {
	t.Helper()
	nodes, err := client.ListNodes(t.Context())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	var names []string
	for _, n := range nodes {
		if n.User != nil && n.User.ID == headscaleUserID {
			names = append(names, n.Name)
		}
	}
	return names
}

// TestUnverifiedEmailCannotAdoptHeadscaleUser: the victim's tailnet login
// left a headscale user carrying their identity, verified email and
// machine, while the console links them to another one. A member who just
// typed the victim's email, e.g. at passkey sign-up, must not be linked to
// it by any of the paths that link members.
func TestUnverifiedEmailCannotAdoptHeadscaleUser(t *testing.T) {
	tests := []struct {
		name string
		link func(t *testing.T, h *Handlers, o realOrg, owner, attacker sqlcgen.User)
	}{
		{"session", func(t *testing.T, h *Handlers, o realOrg, _, attacker sqlcgen.User) {
			rec := httptest.NewRecorder()
			h.Session(rec, withTestUser(httptest.NewRequest(http.MethodGet, "/api/session", nil), attacker))
			if rec.Code != http.StatusOK {
				t.Fatalf("Session status = %d: %s", rec.Code, rec.Body)
			}
		}},
		{"members list", func(t *testing.T, h *Handlers, o realOrg, owner, _ sqlcgen.User) {
			rec := httptest.NewRecorder()
			h.ListMembers(rec, withTestUser(httptest.NewRequest(http.MethodGet, "/api/members", nil), owner))
			if rec.Code != http.StatusOK {
				t.Fatalf("ListMembers status = %d: %s", rec.Code, rec.Body)
			}
		}},
		{"machine registration", func(t *testing.T, h *Handlers, o realOrg, _, attacker sqlcgen.User) {
			if _, err := h.memberHeadscaleUser(t.Context(), o.client, o.org, attacker.ID); err != nil {
				t.Fatalf("memberHeadscaleUser: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, o := newRealOrgTestHandlers(t)
			owner := o.createMember(t, h, RoleOwner, "oidc", "owner@corp.com", true)
			victim := o.createMember(t, h, RoleMember, "oidc", "victim@corp.com", true)
			stolen := o.headscaleUserWithIdentity(t, h, "victim", "victim@corp.com", victim, "victim-laptop")
			other, err := o.client.CreateUser(t.Context(), hscl.CreateUserParams{Name: "victim-oidc"})
			if err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			link(t, h, victim, other.ID)
			attacker := o.createMember(t, h, RoleMember, passkeyProvider, "victim@corp.com", false)

			tt.link(t, h, o, owner, attacker)

			if got := headscaleUserOf(t, h, attacker); got == stolen.ID {
				t.Errorf("attacker linked to the victim's headscale user %s, owning machines %v", got, nodesOf(t, o.client, got))
			}
		})
	}
}

// TestLinkingUsesExistingIdentity: a member's tailnet login left a
// headscale user carrying their identity (without an email, as headscale
// drops unverified ones). Linking the member must pick that user, which
// their tailnet logins keep landing on, instead of creating another.
func TestLinkingUsesExistingIdentity(t *testing.T) {
	h, o := newRealOrgTestHandlers(t)
	member := o.createMember(t, h, RoleMember, passkeyProvider, "member@corp.com", false)
	existing := o.headscaleUserWithIdentity(t, h, "member", "", member, "member-laptop")

	got, err := h.ensureHeadscaleUser(t.Context(), o.org.Subdomain, o.client, member, map[string]bool{})
	if err != nil {
		t.Fatalf("ensureHeadscaleUser: %v", err)
	}

	if got != existing.ID {
		t.Errorf("member linked to headscale user %s, want %s, which owns their machines %v", got, existing.ID, nodesOf(t, o.client, existing.ID))
	}
	users, err := o.client.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("headscale has %d users, want only the existing one: %+v", len(users), users)
	}
}

// TestRemoveMemberRemovesEveryUserWithItsIdentity: removing a member must
// take every machine they can reach the tailnet with off it, including
// those of a headscale user carrying their identity that the console
// doesn't link them to.
func TestRemoveMemberRemovesEveryUserWithItsIdentity(t *testing.T) {
	h, o := newRealOrgTestHandlers(t)
	owner := o.createMember(t, h, RoleOwner, "oidc", "owner@corp.com", true)
	member := o.createMember(t, h, RoleMember, passkeyProvider, "member@corp.com", false)
	withIdentity := o.headscaleUserWithIdentity(t, h, "member", "", member, "member-laptop")
	linked, err := o.client.CreateUser(t.Context(), hscl.CreateUserParams{Name: "member-passkey"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	link(t, h, member, linked.ID)

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/members/x", nil), owner), "id", member.ID)
	rec := httptest.NewRecorder()
	h.RemoveMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("RemoveMember status = %d: %s", rec.Code, rec.Body)
	}

	users, err := o.client.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	for _, u := range users {
		if u.ID == withIdentity.ID {
			t.Errorf("headscale user %s with the removed member's identity survived, with machines %v", u.ID, nodesOf(t, o.client, u.ID))
		}
	}
}

// TestHeadscaleUserGetsOnlyVerifiedEmail: headscale ACL rules can name
// users by email, so a headscale user may only carry an email its owner
// proved they control.
func TestHeadscaleUserGetsOnlyVerifiedEmail(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		verified  bool
		wantEmail string
	}{
		{"sso verified", "oidc", true, "alice@corp.com"},
		{"sso unverified", "oidc", false, ""},
		{"passkey", passkeyProvider, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, o := newRealOrgTestHandlers(t)
			member := o.createMember(t, h, RoleMember, tt.provider, "alice@corp.com", tt.verified)

			hsID, err := h.ensureHeadscaleUser(t.Context(), o.org.Subdomain, o.client, member, map[string]bool{})
			if err != nil {
				t.Fatalf("ensureHeadscaleUser: %v", err)
			}

			users, err := o.client.ListUsers(t.Context())
			if err != nil {
				t.Fatalf("ListUsers: %v", err)
			}
			for _, u := range users {
				if u.ID == hsID && u.Email != tt.wantEmail {
					t.Errorf("headscale user email = %q, want %q", u.Email, tt.wantEmail)
				}
			}
		})
	}
}
