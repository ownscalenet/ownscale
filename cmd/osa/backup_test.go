package main

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/backup"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/orgs"
)

// backUpOrg replicates a database for subdomain, holding a row with
// marker, to replicaDir.
func backUpOrg(t *testing.T, cfg config.BackupConfig, replicaDir, subdomain, marker string) {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	relPath := orgs.DBFile(subdomain)

	pool, err := db.Connect(filepath.Join(dataDir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec("CREATE TABLE marker (value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec("INSERT INTO marker (value) VALUES (?)", marker); err != nil {
		t.Fatal(err)
	}

	r, err := backup.New(ctx, cfg, dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	if err := r.Add(relPath); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !hasFiles(t, filepath.Join(replicaDir, relPath)); {
		if time.Now().After(deadline) {
			t.Fatalf("%s was not replicated in time", relPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func hasFiles(t *testing.T, dir string) bool {
	t.Helper()
	found := false
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			found = true
		}
		return nil
	})
	return found
}

func readMarker(t *testing.T, path string) string {
	t.Helper()
	pool, err := db.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var v string
	if err := pool.QueryRow("SELECT value FROM marker").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifyAndRestoreOrganizations(t *testing.T) {
	ctx := context.Background()
	_, queries := dbtest.New(t)
	dbtest.CreateOrg(t, queries.Queries, "acme")
	dbtest.CreateOrg(t, queries.Queries, "globex")

	replicaDir := t.TempDir()
	cfg := config.BackupConfig{ReplicaURL: "file://" + replicaDir, SyncInterval: 10 * time.Millisecond}
	backUpOrg(t, cfg, replicaDir, "acme", "acme-backup")
	backUpOrg(t, cfg, replicaDir, "globex", "globex-backup")

	// globex is still present locally; acme was lost.
	dataDir := t.TempDir()
	globex, err := db.Connect(filepath.Join(dataDir, orgs.DBFile("globex")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := globex.Exec("CREATE TABLE marker (value TEXT); INSERT INTO marker (value) VALUES ('globex-local')"); err != nil {
		t.Fatal(err)
	}
	globex.Close()

	err = verifyOrganizations(ctx, io.Discard, cfg, dataDir, queries)
	if err == nil || !strings.Contains(err.Error(), "acme") || strings.Contains(err.Error(), "globex") {
		t.Fatalf("verifyOrganizations error = %v, want it to mention only acme", err)
	}

	if err := restoreOrganizations(ctx, io.Discard, cfg, dataDir, queries); err != nil {
		t.Fatalf("restoreOrganizations: %v", err)
	}
	if got := readMarker(t, filepath.Join(dataDir, orgs.DBFile("acme"))); got != "acme-backup" {
		t.Errorf("acme marker = %q, want it restored from backup", got)
	}
	if got := readMarker(t, filepath.Join(dataDir, orgs.DBFile("globex"))); got != "globex-local" {
		t.Errorf("globex marker = %q, want the local database left alone", got)
	}

	if err := verifyOrganizations(ctx, io.Discard, cfg, dataDir, queries); err != nil {
		t.Errorf("verifyOrganizations after restore: %v", err)
	}
}
