package main

import (
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/orgs"
)

func TestListOrganizations(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, queries := dbtest.New(t)
		var out strings.Builder
		if err := listOrganizations(t.Context(), &out, queries); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		if strings.TrimSpace(out.String()) != "no organizations" {
			t.Errorf("output = %q, want %q", out.String(), "no organizations")
		}
	})

	t.Run("lists organizations", func(t *testing.T) {
		_, queries := dbtest.New(t)
		dbtest.CreateOrg(t, queries.Queries, "acme")
		dbtest.CreateOrg(t, queries.Queries, "beta")

		var out strings.Builder
		if err := listOrganizations(t.Context(), &out, queries); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		for _, want := range []string{"acme", "beta", orgs.StatusActive} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output missing %q: %q", want, out.String())
			}
		}
	})
}
