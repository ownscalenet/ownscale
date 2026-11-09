package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/hscl/hstest"
)

// insertUser inserts a user row directly via queries, attached to org with
// the given role and linked to headscale user hsUserID, and returns it.
func insertUser(t *testing.T, queries *sqlcgen.Queries, ctx context.Context, email string, org sqlcgen.Organization, role, hsUserID string) sqlcgen.User {
	t.Helper()
	user, err := queries.CreateUser(ctx, sqlcgen.CreateUserParams{
		ID:              xid.New().String(),
		OrganizationID:  org.ID,
		Role:            role,
		Provider:        "google",
		ProviderUserID:  email,
		Email:           email,
		Name:            "Test User",
		HeadscaleUserID: hsUserID,
	})
	if err != nil {
		t.Fatalf("insertUser(%q): %v", email, err)
	}
	return user
}

func TestListUsers(t *testing.T) {
	t.Run("no users", func(t *testing.T) {
		_, queries := dbtest.New(t)
		var out strings.Builder
		if err := listUsers(t.Context(), &out, queries); err != nil {
			t.Fatalf("listUsers: %v", err)
		}
		if got := out.String(); !strings.Contains(got, "no users") {
			t.Errorf("listUsers output = %q, want it to contain %q", got, "no users")
		}
	})

	t.Run("mix of users", func(t *testing.T) {
		_, queries := dbtest.New(t)

		org := dbtest.CreateOrg(t, queries.Queries, "acme")

		orgUser := insertUser(t, queries.Queries, t.Context(), "owner@acme.test", org, "owner", "1")
		if err := queries.TouchUserLastLogin(t.Context(), orgUser.ID); err != nil {
			t.Fatalf("TouchUserLastLogin: %v", err)
		}

		// A second user in a different organization, with no login yet.
		otherOrg := dbtest.CreateOrg(t, queries.Queries, "beta")
		otherUser := insertUser(t, queries.Queries, t.Context(), "member@beta.test", otherOrg, "member", "7")

		var out strings.Builder
		if err := listUsers(t.Context(), &out, queries); err != nil {
			t.Fatalf("listUsers: %v", err)
		}
		got := out.String()

		for _, want := range []string{
			"ID", "EMAIL", "ORGANIZATION", "ROLE", "PROVIDER", "HEADSCALEID", "LAST LOGIN",
			orgUser.ID, "owner@acme.test", "acme", "owner", "google", "1",
			otherUser.ID, "member@beta.test", "beta", "member", "7",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("listUsers output = %q, want it to contain %q", got, want)
			}
		}

		// The second user's row should show "-" for the still-unset last
		// login column.
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
			t.Errorf("second user's line = %q, want it to show %q for missing last login", otherLine, "-")
		}
	})
}

func TestDeleteUser(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		_, queries := dbtest.New(t)
		var out strings.Builder
		err := deleteUser(t.Context(), &out, queries, nil, "nonexistent-id")
		if err == nil {
			t.Fatal("deleteUser: expected error for nonexistent id, got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent-id") {
			t.Errorf("deleteUser error = %q, want it to mention the id", err)
		}
	})

	t.Run("refuses the last member", func(t *testing.T) {
		_, queries := dbtest.New(t)
		org := dbtest.CreateOrg(t, queries.Queries, "acme")
		user := insertUser(t, queries.Queries, t.Context(), "owner@example.test", org, "owner", "1")
		var out strings.Builder
		err := deleteUser(t.Context(), &out, queries, nil, user.ID)
		if err == nil || !strings.Contains(err.Error(), "last member") {
			t.Fatalf("deleteUser: err = %v, want last member error", err)
		}
		if _, err := queries.GetUserByID(t.Context(), user.ID); err != nil {
			t.Errorf("GetUserByID after refused delete: %v", err)
		}
	})

	t.Run("deletes user, headscale user and machines", func(t *testing.T) {
		_, queries := dbtest.New(t)
		org := dbtest.CreateOrg(t, queries.Queries, "acme")
		sock := hstest.Headscale(t)
		client := hscl.NewUnix(sock)
		hsUserIDs := map[string]string{}
		for _, name := range []string{"linked", "other"} {
			hsUser, err := client.CreateUser(t.Context(), hscl.CreateUserParams{Name: name})
			if err != nil {
				t.Fatalf("CreateUser(%q): %v", name, err)
			}
			hsUserIDs[name] = hsUser.ID
			key := hstest.PendingNode(t, sock, name, name+"-laptop")
			if _, err := client.RegisterNode(t.Context(), name, key); err != nil {
				t.Fatalf("RegisterNode(%q): %v", name, err)
			}
		}
		insertUser(t, queries.Queries, t.Context(), "other@example.test", org, "owner", hsUserIDs["other"])
		user := insertUser(t, queries.Queries, t.Context(), "linked@example.test", org, "member", hsUserIDs["linked"])
		var gotSubdomain string
		clientFor := func(subdomain string) *hscl.Client {
			gotSubdomain = subdomain
			return client
		}

		var out strings.Builder
		if err := deleteUser(t.Context(), &out, queries, clientFor, user.ID); err != nil {
			t.Fatalf("deleteUser: %v", err)
		}
		if gotSubdomain != "acme" {
			t.Errorf("headscale client for %q, want acme", gotSubdomain)
		}
		users, err := client.ListUsers(t.Context())
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if len(users) != 1 || users[0].Name != "other" {
			t.Errorf("headscale users = %+v, want only other", users)
		}
		nodes, err := client.ListNodes(t.Context())
		if err != nil {
			t.Fatalf("ListNodes: %v", err)
		}
		if len(nodes) != 1 || nodes[0].Name != "other-laptop" {
			t.Errorf("headscale nodes = %+v, want only other-laptop", nodes)
		}
		if _, err := queries.GetUserByID(t.Context(), user.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetUserByID after delete: err = %v, want sql.ErrNoRows", err)
		}
	})
}
