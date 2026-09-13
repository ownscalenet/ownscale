package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// insertUser inserts a user row directly via queries, attached to org with
// the given role, and returns it, mirroring the fields orgs/auth would
// normally set through separate calls (CreateUser, then optionally
// LinkUserHeadscaleUser / TouchUserLastLogin).
func insertUser(t *testing.T, queries *sqlcgen.Queries, ctx context.Context, email string, org sqlcgen.Organization, role string) sqlcgen.User {
	t.Helper()
	user, err := queries.CreateUser(ctx, sqlcgen.CreateUserParams{
		ID:             xid.New().String(),
		OrganizationID: org.ID,
		Role:           role,
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

		orgUser := insertUser(t, queries.Queries, t.Context(), "owner@acme.test", org, "owner")
		if err := queries.LinkUserHeadscaleUser(t.Context(), sqlcgen.LinkUserHeadscaleUserParams{
			HeadscaleUserID: sql.NullString{String: "1", Valid: true},
			ID:              orgUser.ID,
		}); err != nil {
			t.Fatalf("LinkUserHeadscaleUser: %v", err)
		}
		if err := queries.TouchUserLastLogin(t.Context(), orgUser.ID); err != nil {
			t.Fatalf("TouchUserLastLogin: %v", err)
		}

		// A second user in a different organization, with no headscale
		// link and no login yet.
		otherOrg := insertOrg(t, queries, "beta")
		otherUser := insertUser(t, queries.Queries, t.Context(), "member@beta.test", otherOrg, "member")

		var out strings.Builder
		if err := listUsers(t.Context(), &out, queries); err != nil {
			t.Fatalf("listUsers: %v", err)
		}
		got := out.String()

		for _, want := range []string{
			"ID", "EMAIL", "ORGANIZATION", "ROLE", "PROVIDER", "HEADSCALEID", "LAST LOGIN",
			orgUser.ID, "owner@acme.test", "acme", "owner", "google", "1",
			otherUser.ID, "member@beta.test", "beta", "member",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("listUsers output = %q, want it to contain %q", got, want)
			}
		}

		// The second user's row should show "-" for the still-unset
		// headscale user id and last login columns.
		otherLine := ""
		for line := range strings.SplitSeq(got, "\n") {
			if strings.Contains(line, "member@beta.test") {
				otherLine = line
			}
		}
		if otherLine == "" {
			t.Fatal("could not find second user's line in output")
		}
		if !strings.Contains(otherLine, "-") {
			t.Errorf("second user's line = %q, want it to show %q for missing headscale id/last login", otherLine, "-")
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
		org := insertOrg(t, queries, "acme")
		user := insertUser(t, queries.Queries, t.Context(), "delete-me@example.test", org, "owner")

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

