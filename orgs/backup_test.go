package orgs

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/backup"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
)

func newReplicator(t *testing.T, dataDir string) (*backup.Replicator, config.BackupConfig, string) {
	t.Helper()
	replicaDir := t.TempDir()
	cfg := config.BackupConfig{ReplicaURL: "file://" + replicaDir, SyncInterval: 10 * time.Millisecond}
	r, err := backup.New(t.Context(), cfg, dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(context.Background()) })
	return r, cfg, replicaDir
}

// waitReplicated waits until relPath has files in replicaDir.
func waitReplicated(t *testing.T, replicaDir, relPath string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		found := false
		filepath.WalkDir(filepath.Join(replicaDir, relPath), func(_ string, d fs.DirEntry, err error) error {
			found = found || (err == nil && d.Type().IsRegular())
			return nil
		})
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was not replicated in time", relPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBackup verifies that an organization's headscale database is
// replicated once headscale starts, keeps replicating across sleeps, and
// is picked up again by a new Service.
func TestBackup(t *testing.T) {
	ctx := t.Context()
	_, queries := dbtest.New(t)
	dataDir := t.TempDir()
	r, cfg, replicaDir := newReplicator(t, dataDir)
	svcCfg := &config.Config{
		BaseDomain:      "example.com",
		DataDir:         dataDir,
		FixedSubdomains: testFixedSubdomains,
	}
	svc := NewService(queries, svcCfg, "https://auth.example.com", slog.New(slog.DiscardHandler), r)
	svc.derpMap.Store(testDERPMap(1))

	if _, err := svc.Create(ctx, "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Ensure(ctx, "acme"); err != nil {
		t.Fatal(err)
	}
	waitReplicated(t, replicaDir, DBFile("acme"))

	// headscale closes and reopens its database while Litestream keeps its
	// own connection open.
	if err := svc.sleepHeadscale(ctx, "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Ensure(ctx, "acme"); err != nil {
		t.Fatal(err)
	}

	if err := svc.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatalf("close replicator: %v", err)
	}

	restoreDir := t.TempDir()
	if err := backup.Restore(ctx, cfg, io.Discard, restoreDir, DBFile("acme")); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	pool, err := db.Connect(filepath.Join(restoreDir, DBFile("acme")))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var policies int
	if err := pool.QueryRow("SELECT COUNT(*) FROM policies").Scan(&policies); err != nil {
		t.Fatal(err)
	}
	if policies != 1 {
		t.Errorf("restored database has %d policies, want the seeded default one", policies)
	}

	r2, _, replicaDir2 := newReplicator(t, dataDir)
	svc2 := NewService(queries, svcCfg, "https://auth.example.com", slog.New(slog.DiscardHandler), r2)
	if err := svc2.BackUpProvisioned(ctx); err != nil {
		t.Fatalf("BackUpProvisioned: %v", err)
	}
	waitReplicated(t, replicaDir2, DBFile("acme"))
}
