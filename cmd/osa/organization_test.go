package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/hscl/hstest"
)

func TestListOrganizations(t *testing.T) {
	statuses := map[string]string{"acme": "running", "beta": "unreachable"}
	probe := func(_ context.Context, subdomain string) string { return statuses[subdomain] }

	t.Run("empty", func(t *testing.T) {
		_, queries := dbtest.New(t)
		var out strings.Builder
		if err := listOrganizations(t.Context(), &out, queries, probe); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		if strings.TrimSpace(out.String()) != "no organizations" {
			t.Errorf("output = %q, want %q", out.String(), "no organizations")
		}
	})

	t.Run("lists organizations with their status", func(t *testing.T) {
		_, queries := dbtest.New(t)
		dbtest.CreateOrg(t, queries.Queries, "acme")
		dbtest.CreateOrg(t, queries.Queries, "beta")

		var out strings.Builder
		if err := listOrganizations(t.Context(), &out, queries, probe); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		lines := strings.Split(out.String(), "\n")
		for subdomain, status := range statuses {
			found := false
			for _, line := range lines {
				fields := strings.Fields(line)
				if len(fields) >= 3 && fields[0] == subdomain {
					found = true
					if fields[2] != status {
						t.Errorf("%s status = %q, want %q", subdomain, fields[2], status)
					}
				}
			}
			if !found {
				t.Errorf("output missing %s: %q", subdomain, out.String())
			}
		}
	})
}

func TestProbeHeadscale(t *testing.T) {
	tests := []struct {
		name string
		sock func(t testing.TB) string
		want string
	}{
		{"running headscale", hstest.Headscale, "running"},
		{"no socket", func(t testing.TB) string { return filepath.Join(t.TempDir(), "missing.sock") }, "unreachable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := probeHeadscale(t.Context(), hscl.NewUnix(tt.sock(t))); got != tt.want {
				t.Errorf("probeHeadscale = %q, want %q", got, tt.want)
			}
		})
	}
}
