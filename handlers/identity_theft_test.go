package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// TestSignupCreatesHeadscaleUserWithIdentity: every new user gets its own
// headscale user, carrying the OIDC identity headscale matches the
// person's tailnet logins by, and the email their login provider reported,
// verified or not.
func TestSignupCreatesHeadscaleUserWithIdentity(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		email    string
		verified bool
	}{
		{"sso verified", "google", "alice@corp.com", true},
		{"sso unverified", "oidc", "alice@corp.com", false},
		{"microsoft", "microsoft", "alice@corp.com", false},
		{"passkey", passkeyProvider, passkeyEmail("alice"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, o := newRealOrgTestHandlers(t)
			member := o.createMember(t, h, RoleMember, tt.provider, tt.email, tt.verified)

			hsUser := o.headscaleUser(t, member.HeadscaleUserID)
			if want := h.orgs.Identifier(member.ID); hsUser.ProviderID != want {
				t.Errorf("headscale user identity = %q, want %q", hsUser.ProviderID, want)
			}
			if hsUser.Email != tt.email {
				t.Errorf("headscale user email = %q, want %q", hsUser.Email, tt.email)
			}
		})
	}
}

// TestSpoofedEmailGetsOwnHeadscaleUser: a sign-up reporting another
// member's email, whether its provider verified it or not, gets a
// headscale user of its own, and the other member keeps their machines.
func TestSpoofedEmailGetsOwnHeadscaleUser(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		verified bool
	}{
		{"unverified oidc", "oidc", false},
		{"microsoft", "microsoft", false},
		{"verified sso", "google", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, o := newRealOrgTestHandlers(t)
			victim := o.createMember(t, h, RoleMember, "google", "victim@corp.com", true)
			o.registerMachine(t, victim, "victim-laptop")

			attacker := o.createMember(t, h, RoleMember, tt.provider, "victim@corp.com", tt.verified)

			if attacker.HeadscaleUserID == victim.HeadscaleUserID {
				t.Fatalf("attacker got the victim's headscale user %s", victim.HeadscaleUserID)
			}
			if got := nodesOf(t, o.client, victim.HeadscaleUserID); len(got) != 1 || got[0] != "victim-laptop" {
				t.Errorf("victim's machines = %v, want [victim-laptop]", got)
			}
			if got := nodesOf(t, o.client, attacker.HeadscaleUserID); len(got) != 0 {
				t.Errorf("attacker's machines = %v, want none", got)
			}
		})
	}
}

// TestPasskeySignupCannotClaimAnothersEmail: a passkey sign-up into the
// victim's organization, choosing the victim's email local part as its
// username, gets its own headscale user through the real registration flow.
func TestPasskeySignupCannotClaimAnothersEmail(t *testing.T) {
	setupSessions()
	h, o := newRealOrgTestHandlers(t)
	owner := o.createMember(t, h, RoleOwner, "google", "owner@corp.com", true)
	victim := o.createMember(t, h, RoleMember, "google", "victim@corp.com", true)
	o.registerMachine(t, victim, "victim-laptop")
	inviteInto(t, h.queries, o.org.ID, owner.ID, "ste0token")

	if rec := passkeySignup(t, h, "victim", "ste0token"); rec.Code != http.StatusSeeOther {
		t.Fatalf("finish status = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body)
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
	if attacker.HeadscaleUserID == victim.HeadscaleUserID {
		t.Errorf("attacker linked to the victim's headscale user %s, owning machines %v", victim.HeadscaleUserID, nodesOf(t, o.client, victim.HeadscaleUserID))
	}
}

// TestConcurrentSignupsOnOneInvite: of several sign-ups racing for one
// invite, only one gets an account, and the others' headscale users are
// deleted again.
func TestConcurrentSignupsOnOneInvite(t *testing.T) {
	setupSessions()
	h, o := newRealOrgTestHandlers(t)
	owner := o.createMember(t, h, RoleOwner, "google", "owner@corp.com", true)
	inviteInto(t, h.queries, o.org.ID, owner.ID, "racetoken")

	rp := testRelyingParty(testBaseDomain)
	const n = 4
	finishReqs := make([]*http.Request, n)
	for i := range finishReqs {
		body := `{"username":"racer` + string(rune('a'+i)) + `","next":"/invite/racetoken"}`
		beginRec := httptest.NewRecorder()
		h.PasskeyRegisterBegin(beginRec, httptest.NewRequest(http.MethodPost, "/api/passkey/register/begin", strings.NewReader(body)))
		if beginRec.Code != http.StatusOK {
			t.Fatalf("begin status = %d: %s", beginRec.Code, beginRec.Body)
		}
		build := registerBuilder(rp, vwa.NewAuthenticator(), vwa.NewCredential(vwa.KeyTypeEC2))
		finishReqs[i] = httptest.NewRequest(http.MethodPost, "/finish", strings.NewReader(build(beginRec.Body.String())))
		for _, c := range beginRec.Result().Cookies() {
			finishReqs[i].AddCookie(c)
		}
	}

	codes := make([]int, n)
	var wg sync.WaitGroup
	for i, req := range finishReqs {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			h.PasskeyRegisterFinish(rec, req)
			codes[i] = rec.Code
		})
	}
	wg.Wait()

	var created int
	for _, code := range codes {
		switch code {
		case http.StatusSeeOther:
			created++
		case http.StatusGone:
		default:
			t.Errorf("sign-up status = %d, want 303 or 410", code)
		}
	}
	if created != 1 {
		t.Errorf("%d sign-ups succeeded, want 1 (statuses %v)", created, codes)
	}
	members, err := h.queries.ListUsersByOrganization(t.Context(), o.org.ID)
	if err != nil {
		t.Fatalf("ListUsersByOrganization: %v", err)
	}
	hsUsers, err := o.client.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	// The organization's members plus the funnel's user, if it has one.
	linked := map[string]bool{}
	for _, m := range members {
		linked[m.HeadscaleUserID] = true
	}
	for _, u := range hsUsers {
		if !linked[u.ID] && u.ProviderID != "" {
			t.Errorf("headscale user %+v of a failed sign-up survived", u)
		}
	}
	if len(members) != 2 {
		t.Errorf("organization has %d members, want the owner and one invitee", len(members))
	}
}

// TestSignupWithoutHeadscale: a sign-up whose organization's headscale
// can't start fails with 503, without creating an account.
func TestSignupWithoutHeadscale(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlersWithBaseDomain(t, testBaseDomain)
	owner := insertUser(t, queries)
	inviteInto(t, queries, owner.OrganizationID, owner.ID, "downtoken")
	h.hs = fakeHeadscaleClients{err: errors.New("headscale failed to start")}

	if rec := passkeySignup(t, h, "newcomer", "downtoken"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body)
	}
	for _, u := range listAllUsers(t, queries) {
		if u.Name == "newcomer" {
			t.Errorf("user %+v created without a headscale user", u)
		}
	}
	inv, err := queries.GetInvite(t.Context(), "invite-downtoken")
	if err != nil {
		t.Fatalf("GetInvite: %v", err)
	}
	if inv.AcceptedAt.Valid || inv.ExpiresAt.Before(time.Now()) {
		t.Errorf("invite = %+v, want it still pending", inv)
	}
}
