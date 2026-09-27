package handlers

import (
	"database/sql"
	"testing"

	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
)

func TestEmailLocalPart(t *testing.T) {
	cases := map[string]string{
		"alice@example.com": "alice",
		"Alice@Example.com": "alice",
		"no-at-sign":        "",
		"@example.com":      "",
	}
	for email, want := range cases {
		if got := emailLocalPart(email); got != want {
			t.Errorf("emailLocalPart(%q) = %q, want %q", email, got, want)
		}
	}
}

func TestShortID(t *testing.T) {
	got := shortID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if got != "aaaaaaaa" {
		t.Errorf("shortID = %q, want first 8 chars with dashes stripped", got)
	}
	if got := shortID("short"); got != "short" {
		t.Errorf("shortID(short id) = %q, want unchanged", got)
	}
}

func TestClaimedHeadscaleUserIDs(t *testing.T) {
	members := []sqlcgen.User{
		{ID: "a", HeadscaleUserID: sql.NullString{String: "1", Valid: true}},
		{ID: "b", HeadscaleUserID: sql.NullString{}},
		{ID: "c", HeadscaleUserID: sql.NullString{String: "2", Valid: true}},
	}
	claimed := claimedHeadscaleUserIDs(members)
	if len(claimed) != 2 || !claimed["1"] || !claimed["2"] {
		t.Errorf("claimed = %+v, want {1, 2}", claimed)
	}
}

func TestUniqueHeadscaleName(t *testing.T) {
	existing := []hscl.User{{ID: "1", Name: "alice"}, {ID: "2", Name: "alice-google"}}

	if got := uniqueHeadscaleName("bob", "google", "user-id", existing); got != "bob" {
		t.Errorf("no collision: got %q, want %q", got, "bob")
	}
	if got := uniqueHeadscaleName("alice", "microsoft", "user-id", existing); got != "alice-microsoft" {
		t.Errorf("candidate collides: got %q, want provider-qualified %q", got, "alice-microsoft")
	}
	if got := uniqueHeadscaleName("alice", "google", "12345678-abcd", existing); got != "alice-google-12345678" {
		t.Errorf("candidate and provider-qualified both collide: got %q, want id-qualified %q", got, "alice-google-12345678")
	}
}

// newHeadscaleUsersTestHandlers builds Handlers, a queries handle, and a
// fake headscale server (see newFakeHeadscaleServer in machines_test.go)
// carrying headscale users "1"/alice@example.com and "2"/bob@example.com.
func newHeadscaleUsersTestHandlers(t *testing.T) (*Handlers, *dbpkg.TxQueries, *hscl.Client) {
	t.Helper()
	h, queries := newTestHandlers(t)
	client, _, _, _ := newFakeHeadscaleServer(t)
	return h, queries, client
}

func TestEnsureHeadscaleUserAlreadyLinked(t *testing.T) {
	h, _, _ := newHeadscaleUsersTestHandlers(t)
	user := sqlcgen.User{ID: "u1", HeadscaleUserID: sql.NullString{String: "99", Valid: true}}

	// Passing a nil client confirms the already-linked short circuit
	// never touches headscale at all.
	got, err := h.ensureHeadscaleUser(t.Context(), "acme", nil, user, nil)
	if err != nil {
		t.Fatalf("ensureHeadscaleUser: %v", err)
	}
	if got != "99" {
		t.Errorf("headscaleUserID = %q, want %q", got, "99")
	}
}

func TestEnsureHeadscaleUserMatchesByEmail(t *testing.T) {
	h, queries, client := newHeadscaleUsersTestHandlers(t)
	user := insertUser(t, queries)
	if err := queries.UpdateUserProfile(t.Context(), sqlcgen.UpdateUserProfileParams{
		Email: "alice@example.com", Name: user.Name, AvatarUrl: user.AvatarUrl,
		EmailVerified: user.EmailVerified, ID: user.ID,
	}); err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}
	user, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	got, err := h.ensureHeadscaleUser(t.Context(), "acme", client, user, map[string]bool{})
	if err != nil {
		t.Fatalf("ensureHeadscaleUser: %v", err)
	}
	if got != "1" {
		t.Errorf("headscaleUserID = %q, want matched alice (1)", got)
	}
	linked, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if linked.HeadscaleUserID.String != "1" {
		t.Errorf("persisted headscaleUserId = %+v, want 1", linked.HeadscaleUserID)
	}
}

func TestEnsureHeadscaleUserSkipsClaimedAndCreatesInstead(t *testing.T) {
	h, queries, client := newHeadscaleUsersTestHandlers(t)
	user := insertUser(t, queries)
	if err := queries.UpdateUserProfile(t.Context(), sqlcgen.UpdateUserProfileParams{
		Email: "alice@example.com", Name: user.Name, AvatarUrl: user.AvatarUrl,
		EmailVerified: user.EmailVerified, ID: user.ID,
	}); err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}
	user, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	// Another org member already claimed headscale user "1" (alice) --
	// this ownscale identity must not steal it, even though its email
	// also matches.
	got, err := h.ensureHeadscaleUser(t.Context(), "acme", client, user, map[string]bool{"1": true})
	if err != nil {
		t.Fatalf("ensureHeadscaleUser: %v", err)
	}
	if got == "1" {
		t.Errorf("headscaleUserID = %q, must not reuse claimed user 1", got)
	}
	if got != "3" {
		t.Errorf("headscaleUserID = %q, want newly created user 3 (see newFakeHeadscaleServer)", got)
	}
	linked, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if linked.HeadscaleUserID.String != "3" {
		t.Errorf("persisted headscaleUserId = %+v, want 3", linked.HeadscaleUserID)
	}
}
