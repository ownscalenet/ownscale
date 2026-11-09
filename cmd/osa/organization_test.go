package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/hscl/hstest"
	"github.com/ownscalenet/ownscale/orgs"
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
		name       string
		running    bool
		delete     []string
		wantErr    bool
		wantRemain []string
	}{
		{"deletes data and backups", false, []string{"acme"}, false, []string{"globex"}},
		{"refuses while ownscale runs", true, []string{"acme"}, true, []string{"acme", "globex"}},
		{"stops at an unknown organization", false, []string{"acme", "nope", "globex"}, true, []string{"globex"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replicaDir := t.TempDir()
			bcfg := config.BackupConfig{ReplicaURL: "file://" + replicaDir, SyncInterval: 10 * time.Millisecond}
			cfg := &config.Config{DataDir: t.TempDir(), Backup: bcfg}
			_, queries := dbtest.New(t)
			for _, sub := range []string{"acme", "globex"} {
				dbtest.CreateOrg(t, queries.Queries, sub)
				backUpOrg(t, bcfg, replicaDir, sub, sub)
				if err := os.MkdirAll(filepath.Join(cfg.DataDir, sub), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tt.running {
				unlock, err := orgs.LockDataDir(cfg.DataDir)
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
			}

			err := deleteOrganizations(t.Context(), io.Discard, cfg, queries, tt.delete)
			if (err != nil) != tt.wantErr {
				t.Fatalf("deleteOrganizations err = %v, wantErr %v", err, tt.wantErr)
			}

			var remain []string
			for _, sub := range []string{"acme", "globex"} {
				_, dbErr := queries.GetOrganizationBySubdomain(t.Context(), sub)
				_, dirErr := os.Stat(filepath.Join(cfg.DataDir, sub))
				backedUp := hasFiles(t, filepath.Join(replicaDir, orgs.DBFile(sub)))
				if (dbErr == nil) != (dirErr == nil) || (dbErr == nil) != backedUp {
					t.Errorf("%s: row err %v, dir err %v, backed up %v; want all present or all gone", sub, dbErr, dirErr, backedUp)
				}
				if dbErr == nil {
					remain = append(remain, sub)
				}
			}
			if !slices.Equal(remain, tt.wantRemain) {
				t.Errorf("remaining = %v, want %v", remain, tt.wantRemain)
			}
		})
	}
}
