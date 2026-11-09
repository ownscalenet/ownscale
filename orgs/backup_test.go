package orgs

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
		FastOrgs:        true,
		BaseDomain:      "example.com",
		DataDir:         dataDir,
		FixedSubdomains: testFixedSubdomains,
		ContentDomain:   "example.com",
	}
	svc, err := NewService(queries, svcCfg, "https://auth.example.com", slog.New(slog.DiscardHandler), r)
	if err != nil {
		t.Fatal(err)
	}
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
	svc2, err := NewService(queries, svcCfg, "https://auth.example.com", slog.New(slog.DiscardHandler), r2)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc2.BackUpAll(ctx); err != nil {
		t.Fatalf("BackUpAll: %v", err)
	}
	waitReplicated(t, replicaDir2, DBFile("acme"))
}

// TestDelete verifies that deleting an organization stops its headscale and
// removes its rows, files and backup, so its subdomain can be reused.
func TestDelete(t *testing.T) {
	ctx := t.Context()
	_, queries := dbtest.New(t)
	dataDir := t.TempDir()
	r, _, replicaDir := newReplicator(t, dataDir)
	svc, err := NewService(queries, &config.Config{
		FastOrgs:      true,
		BaseDomain:    "example.com",
		DataDir:       dataDir,
		ContentDomain: "example.com",
	}, "https://auth.example.com", slog.New(slog.DiscardHandler), r)
	if err != nil {
		t.Fatal(err)
	}
	svc.derpMap.Store(testDERPMap(1))
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	var slept []string
	svc.OnSleep(func(subdomain string) { slept = append(slept, subdomain) })

	org, err := svc.Create(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	inst, ok := svc.running("acme")
	if !ok {
		t.Fatal("acme isn't running after Create")
	}
	waitReplicated(t, replicaDir, DBFile("acme"))

	if err := svc.ForOrg(org).Delete(ctx); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	select {
	case <-inst.exited:
	default:
		t.Error("headscale still running after Delete")
	}
	if !slices.Equal(slept, []string{"acme"}) {
		t.Errorf("OnSleep calls = %v, want [acme]", slept)
	}
	if _, err := queries.GetOrganization(ctx, org.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetOrganization: err = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.GetOIDCClientByOrganizationID(ctx, org.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetOIDCClientByOrganizationID: err = %v, want sql.ErrNoRows", err)
	}
	for _, dir := range []string{filepath.Join(dataDir, "acme"), filepath.Join(replicaDir, DBFile("acme"))} {
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%s): err = %v, want it removed", dir, err)
		}
	}
	if _, err := svc.Ensure(ctx, "acme"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Ensure after Delete: err = %v, want ErrNotRunning", err)
	}

	if _, err := svc.Create(ctx, "acme"); err != nil {
		t.Fatalf("Create after Delete: %v", err)
	}
	waitReplicated(t, replicaDir, DBFile("acme"))
}
