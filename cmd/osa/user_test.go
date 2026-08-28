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
