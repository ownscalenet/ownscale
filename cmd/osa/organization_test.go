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
	results := map[string]probeResult{
		"acme": {status: "running", machines: 12, online: 3},
		"beta": {status: "unreachable"},
	}
	probe := func(_ context.Context, subdomain string) probeResult { return results[subdomain] }

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

	t.Run("lists organizations with machine counts and status", func(t *testing.T) {
		_, queries := dbtest.New(t)
		dbtest.CreateOrg(t, queries.Queries, "acme")
		dbtest.CreateOrg(t, queries.Queries, "beta")

		var out strings.Builder
		if err := listOrganizations(t.Context(), &out, queries, probe); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		want := map[string]string{
			"acme": "acme                 0      12        3        running",
			"beta": "beta                 0      -         -        unreachable",
		}
		for subdomain, prefix := range want {
			found := false
			for line := range strings.Lines(out.String()) {
				if strings.HasPrefix(line, subdomain+" ") {
					found = true
					if !strings.HasPrefix(line, prefix) {
						t.Errorf("line = %q, want prefix %q", line, prefix)
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
	withNode := func(t testing.TB) string {
		sock := hstest.Headscale(t)
		c := hscl.NewUnix(sock)
		if _, err := c.CreateUser(t.Context(), hscl.CreateUserParams{Name: "alice"}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.RegisterNode(t.Context(), "alice", hstest.PendingNode(t, sock, "alice", "laptop")); err != nil {
			t.Fatal(err)
		}
		return sock
	}
	tests := []struct {
		name string
		sock func(t testing.TB) string
		want probeResult
	}{
		{"no nodes", hstest.Headscale, probeResult{status: "running"}},
		{"one offline node", withNode, probeResult{status: "running", machines: 1}},
		{"no socket", func(t testing.TB) string { return filepath.Join(t.TempDir(), "missing.sock") }, probeResult{status: "unreachable"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := probeHeadscale(t.Context(), hscl.NewUnix(tt.sock(t))); got != tt.want {
				t.Errorf("probeHeadscale = %+v, want %+v", got, tt.want)
			}
		})
	}
}
