package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/control"
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

func TestListOrganizationSubdomains(t *testing.T) {
	tests := []struct {
		name string
		orgs []string
		want string
	}{
		{"empty", nil, ""},
		{"sorted by subdomain", []string{"beta", "acme"}, "acme\nbeta\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, queries := dbtest.New(t)
			for _, sub := range tt.orgs {
				dbtest.CreateOrg(t, queries.Queries, sub)
			}
			var out strings.Builder
			if err := listOrganizationSubdomains(t.Context(), &out, queries); err != nil {
				t.Fatalf("listOrganizationSubdomains: %v", err)
			}
			if out.String() != tt.want {
				t.Errorf("output = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestDeleteOrganizations(t *testing.T) {
	tests := []struct {
		name        string
		serving     bool
		delete      []string
		wantErr     bool
		wantDeleted []string
	}{
		{"deletes through the server", true, []string{"acme", "globex"}, false, []string{"acme", "globex"}},
		{"stops at an unknown organization", true, []string{"acme", "nope", "globex"}, true, []string{"acme"}},
		{"ownscale not running", false, []string{"acme"}, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Unix socket paths are limited to ~100 bytes.
			dataDir, err := os.MkdirTemp("", "osa")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dataDir) })
			var deleted []string
			if tt.serving {
				deleteOrg := func(_ context.Context, sub string) error {
					if sub != "acme" && sub != "globex" {
						return sql.ErrNoRows
					}
					deleted = append(deleted, sub)
					return nil
				}
				serveControl(t, dataDir, deleteOrg)
			}

			var out strings.Builder
			err = deleteOrganizations(t.Context(), &out, control.NewClient(dataDir), tt.delete)
			if (err != nil) != tt.wantErr {
				t.Fatalf("deleteOrganizations err = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(deleted, tt.wantDeleted) {
				t.Errorf("deleted = %v, want %v", deleted, tt.wantDeleted)
			}
			var wantOut string
			for _, sub := range tt.wantDeleted {
				wantOut += "deleted " + sub + "\n"
			}
			if out.String() != wantOut {
				t.Errorf("output = %q, want %q", out.String(), wantOut)
			}
		})
	}
}

// serveControl serves the control socket in dataDir like a running
// ownscale, deleting organizations with deleteOrg.
func serveControl(t *testing.T, dataDir string, deleteOrg func(context.Context, string) error) {
	t.Helper()
	ln, err := net.Listen("unix", control.SocketPath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: control.Handler(deleteOrg, slog.New(slog.DiscardHandler))}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}
