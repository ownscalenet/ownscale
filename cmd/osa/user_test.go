package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/markbates/goth/gothic"
	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/orgs"
)

// insertUser inserts a user row directly via queries and returns it,
// mirroring the fields orgs/auth would normally set through separate
// calls (CreateUser, then optionally SetUserOrganizationAndRole /
// LinkUserHeadscaleUser / TouchUserLastLogin).
func insertUser(t *testing.T, queries *sqlcgen.Queries, ctx context.Context, email string) sqlcgen.User {
	t.Helper()
	user, err := queries.CreateUser(ctx, sqlcgen.CreateUserParams{
		ID:             xid.New().String(),
		Provider:       "google",
		ProviderUserID: email,
		Email:          email,
		Name:           "Test User",
	})
	if err != nil {
		t.Fatalf("insertUser(%q): %v", email, err)
	}
	return user
}

func TestListUsers(t *testing.T) {
	t.Run("no users", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		if err := listUsers(t.Context(), &out, queries); err != nil {
			t.Fatalf("listUsers: %v", err)
		}
		if got := out.String(); !strings.Contains(got, "no users") {
			t.Errorf("listUsers output = %q, want it to contain %q", got, "no users")
		}
	})

	t.Run("mix of users", func(t *testing.T) {
		_, queries := newTestDB(t)

		org := insertOrg(t, queries, "acme")

		orgUser := insertUser(t, queries.Queries, t.Context(), "owner@acme.test")
		if err := queries.SetUserOrganizationAndRole(t.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
			OrganizationID: sql.NullString{String: org.ID, Valid: true},
			Role:           "owner",
			ID:             orgUser.ID,
		}); err != nil {
			t.Fatalf("SetUserOrganizationAndRole: %v", err)
		}
		if err := queries.LinkUserHeadscaleUser(t.Context(), sqlcgen.LinkUserHeadscaleUserParams{
			HeadscaleUserID: sql.NullString{String: "1", Valid: true},
			ID:              orgUser.ID,
		}); err != nil {
			t.Fatalf("LinkUserHeadscaleUser: %v", err)
		}
		if err := queries.TouchUserLastLogin(t.Context(), orgUser.ID); err != nil {
			t.Fatalf("TouchUserLastLogin: %v", err)
		}

		// A user with no organization (e.g. removed, or never joined
		// one), no headscale link, and no login yet.
		insertUser(t, queries.Queries, t.Context(), "orphan@example.test")

		var out strings.Builder
		if err := listUsers(t.Context(), &out, queries); err != nil {
			t.Fatalf("listUsers: %v", err)
		}
		got := out.String()

		for _, want := range []string{
			"ID", "EMAIL", "ORGANIZATION", "ROLE", "PROVIDER", "HEADSCALEID", "LAST LOGIN",
			orgUser.ID, "owner@acme.test", "acme", "owner", "google", "1",
			"orphan@example.test",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("listUsers output = %q, want it to contain %q", got, want)
			}
		}

		// The orphaned user's organization column should show "-" since
		// it has no organization_id.
		orphanLine := ""
		for line := range strings.SplitSeq(got, "\n") {
			if strings.Contains(line, "orphan@example.test") {
				orphanLine = line
			}
		}
		if orphanLine == "" {
			t.Fatal("could not find orphan user's line in output")
		}
		if !strings.Contains(orphanLine, "-") {
			t.Errorf("orphan user line = %q, want it to show %q for missing organization/last login", orphanLine, "-")
		}
	})
}

func TestDeleteUser(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		err := deleteUser(t.Context(), &out, queries, "nonexistent-id")
		if err == nil {
			t.Fatal("deleteUser: expected error for nonexistent id, got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent-id") {
			t.Errorf("deleteUser error = %q, want it to mention the id", err)
		}
	})

	t.Run("deletes existing user", func(t *testing.T) {
		_, queries := newTestDB(t)
		user := insertUser(t, queries.Queries, t.Context(), "delete-me@example.test")

		var out strings.Builder
		if err := deleteUser(t.Context(), &out, queries, user.ID); err != nil {
			t.Fatalf("deleteUser: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, user.ID) || !strings.Contains(got, "delete-me@example.test") {
			t.Errorf("deleteUser output = %q, want it to mention the deleted user's id and email", got)
		}

		if _, err := queries.GetUserByID(t.Context(), user.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetUserByID after delete: err = %v, want sql.ErrNoRows", err)
		}
	})
}

func sessionUserFromCookie(t *testing.T, line string) string {
	t.Helper()
	name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		t.Fatalf("cookie line %q not in name=value form", line)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	req.AddCookie(&http.Cookie{Name: name, Value: value})
	sess, err := gothic.Store.Get(req, gothic.SessionName)
	if err != nil {
		t.Fatalf("decode session cookie: %v", err)
	}
	userID, _ := sess.Values[auth.SessionUserKey].(string)
	return userID
}

func TestLoginUser(t *testing.T) {
	auth.Setup("test-session-secret")

	t.Run("creates a new user and mints a valid session", func(t *testing.T) {
		_, queries := newTestDB(t)

		var out strings.Builder
		if err := loginUser(t.Context(), &out, queries, "new@test.example", "", "", "owner"); err != nil {
			t.Fatalf("loginUser: %v", err)
		}

		user, err := queries.GetUserByProvider(t.Context(), sqlcgen.GetUserByProviderParams{
			Provider:       testAuthProvider,
			ProviderUserID: "new@test.example",
		})
		if err != nil {
			t.Fatalf("GetUserByProvider: %v", err)
		}
		if user.Name != "new@test.example" {
			t.Errorf("user.Name = %q, want it to default to the email", user.Name)
		}
		if !user.LastLoginAt.Valid {
			t.Error("user.LastLoginAt not set, want loginUser to touch it")
		}
		if user.OrganizationID.Valid {
			t.Errorf("user.OrganizationID = %+v, want unset since no --org was given", user.OrganizationID)
		}

		if got := sessionUserFromCookie(t, out.String()); got != user.ID {
			t.Errorf("minted session user id = %q, want %q", got, user.ID)
		}
	})

	t.Run("reuses the existing user on a second login", func(t *testing.T) {
		_, queries := newTestDB(t)

		var out1, out2 strings.Builder
		if err := loginUser(t.Context(), &out1, queries, "again@test.example", "", "", "owner"); err != nil {
			t.Fatalf("loginUser (1st): %v", err)
		}
		if err := loginUser(t.Context(), &out2, queries, "again@test.example", "", "", "owner"); err != nil {
			t.Fatalf("loginUser (2nd): %v", err)
		}

		if sessionUserFromCookie(t, out1.String()) != sessionUserFromCookie(t, out2.String()) {
			t.Error("loginUser created a second distinct user for the same email instead of reusing it")
		}
	})

	t.Run("attaches to an already-active organization", func(t *testing.T) {
		_, queries := newTestDB(t)
		org := insertOrg(t, queries, "acme")
		if _, err := queries.UpdateOrganizationStatus(t.Context(), sqlcgen.UpdateOrganizationStatusParams{
			Status: orgs.StatusActive,
			ID:     org.ID,
		}); err != nil {
			t.Fatalf("UpdateOrganizationStatus: %v", err)
		}

		var out strings.Builder
		if err := loginUser(t.Context(), &out, queries, "owner@test.example", "", "acme", "owner"); err != nil {
			t.Fatalf("loginUser: %v", err)
		}

		user, err := queries.GetUserByProvider(t.Context(), sqlcgen.GetUserByProviderParams{
			Provider:       testAuthProvider,
			ProviderUserID: "owner@test.example",
		})
		if err != nil {
			t.Fatalf("GetUserByProvider: %v", err)
		}
		if !user.OrganizationID.Valid || user.OrganizationID.String != org.ID {
			t.Errorf("user.OrganizationID = %+v, want %q", user.OrganizationID, org.ID)
		}
		if user.Role != "owner" {
			t.Errorf("user.Role = %q, want %q", user.Role, "owner")
		}
	})

	t.Run("errors for an unknown organization", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		err := loginUser(t.Context(), &out, queries, "someone@test.example", "", "does-not-exist", "owner")
		if err == nil {
			t.Fatal("loginUser: expected error for unknown organization, got nil")
		}
		if !strings.Contains(err.Error(), "does-not-exist") {
			t.Errorf("loginUser error = %q, want it to mention the subdomain", err)
		}
	})

	t.Run("errors for a not-yet-active organization", func(t *testing.T) {
		_, queries := newTestDB(t)
		insertOrg(t, queries, "pending-org")

		var out strings.Builder
		err := loginUser(t.Context(), &out, queries, "someone@test.example", "", "pending-org", "owner")
		if err == nil {
			t.Fatal("loginUser: expected error for a pending organization, got nil")
		}
		if !strings.Contains(err.Error(), "not active") {
			t.Errorf("loginUser error = %q, want it to mention the organization isn't active", err)
		}
	})

	t.Run("does not reattach a user who already has an organization", func(t *testing.T) {
		_, queries := newTestDB(t)
		firstOrg := insertOrg(t, queries, "first-org")
		if _, err := queries.UpdateOrganizationStatus(t.Context(), sqlcgen.UpdateOrganizationStatusParams{
			Status: orgs.StatusActive,
			ID:     firstOrg.ID,
		}); err != nil {
			t.Fatalf("UpdateOrganizationStatus: %v", err)
		}
		secondOrg := insertOrg(t, queries, "second-org")
		if _, err := queries.UpdateOrganizationStatus(t.Context(), sqlcgen.UpdateOrganizationStatusParams{
			Status: orgs.StatusActive,
			ID:     secondOrg.ID,
		}); err != nil {
			t.Fatalf("UpdateOrganizationStatus: %v", err)
		}

		var out strings.Builder
		if err := loginUser(t.Context(), &out, queries, "member@test.example", "", "first-org", "owner"); err != nil {
			t.Fatalf("loginUser (1st): %v", err)
		}
		if err := loginUser(t.Context(), &out, queries, "member@test.example", "", "second-org", "owner"); err != nil {
			t.Fatalf("loginUser (2nd): %v", err)
		}

		user, err := queries.GetUserByProvider(t.Context(), sqlcgen.GetUserByProviderParams{
			Provider:       testAuthProvider,
			ProviderUserID: "member@test.example",
		})
		if err != nil {
			t.Fatalf("GetUserByProvider: %v", err)
		}
		if user.OrganizationID.String != firstOrg.ID {
			t.Errorf("user.OrganizationID = %q, want it to keep the first org %q", user.OrganizationID.String, firstOrg.ID)
		}
	})
}
