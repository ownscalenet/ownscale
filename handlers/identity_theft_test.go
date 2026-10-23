package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	vwa "github.com/descope/virtualwebauthn"

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

// TestPasskeySignupCannotClaimAnothersEmail: a passkey sign-up can't assert
// an email at all (it only picks a username, stored as username@passkey), so
// it can never match -- and therefore adopt -- a victim's headscale user,
// which carries the victim's real verified email. The attacker signs up
// through the real registration flow, into the victim's org via an invite.
func TestPasskeySignupCannotClaimAnothersEmail(t *testing.T) {
	setupSessions()
	h, o := newRealOrgTestHandlers(t)

	owner := o.createMember(t, h, RoleOwner, "oidc", "owner@corp.com", true)
	victim := o.createMember(t, h, RoleMember, "oidc", "victim@corp.com", true)
	stolen := o.headscaleUserWithIdentity(t, h, "victim", "victim@corp.com", victim, "victim-laptop")

	if _, err := h.queries.CreateInvite(t.Context(), sqlcgen.CreateInviteParams{
		ID:             "invite-steal-1",
		OrganizationID: o.org.ID,
		Role:           RoleMember,
		Token:          "ste0token",
		CreatedBy:      owner.ID,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	// The attacker tries to take the victim's identity by choosing a
	// username matching the victim's email local part.
	rp := testRelyingParty(testBaseDomain)
	beginReq := httptest.NewRequest(http.MethodPost, "/api/passkey/register/begin",
		strings.NewReader(`{"username":"victim","next":"/invite/ste0token"}`))
	finishRec := runCeremony(t, h.PasskeyRegisterBegin, h.PasskeyRegisterFinish, beginReq,
		registerBuilder(rp, vwa.NewAuthenticator(), vwa.NewCredential(vwa.KeyTypeEC2)))
	if finishRec.Code != http.StatusSeeOther {
		t.Fatalf("finish status = %d, want %d: %s", finishRec.Code, http.StatusSeeOther, finishRec.Body)
	}

	var attacker sqlcgen.User
	for _, u := range listAllUsers(t, h.queries) {
		if u.Provider == passkeyProvider && u.Name == "victim" {
			attacker = u
		}
	}
	if attacker.ID == "" {
		t.Fatal("expected the passkey attacker account to be created")
	}
	if attacker.Email != "victim@passkey" {
		t.Errorf("attacker email = %q, want victim@passkey (no real email can be asserted)", attacker.Email)
	}

	// Linking the attacker (as /api/session does) must not hand them the
	// victim's headscale user and its machines.
	rec := httptest.NewRecorder()
	h.Session(rec, withTestUser(httptest.NewRequest(http.MethodGet, "/api/session", nil), attacker))
	if rec.Code != http.StatusOK {
		t.Fatalf("Session status = %d: %s", rec.Code, rec.Body)
	}
	if got := headscaleUserOf(t, h, attacker); got == stolen.ID {
		t.Errorf("attacker linked to the victim's headscale user %s, owning machines %v", got, nodesOf(t, o.client, got))
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
