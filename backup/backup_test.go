package backup

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/benbjohnson/litestream/abs"
	"github.com/benbjohnson/litestream/file"
	"github.com/benbjohnson/litestream/s3"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
)

var discardLogger = slog.New(slog.DiscardHandler)

func fileConfig(t *testing.T) config.BackupConfig {
	t.Helper()
	return config.BackupConfig{
		ReplicaURL:   "file://" + t.TempDir(),
		SyncInterval: 10 * time.Millisecond,
	}
}

// writeNames creates dataDir/relPath holding names.
func writeNames(t *testing.T, dataDir, relPath string, names ...string) *sql.DB {
	t.Helper()
	pool, err := db.Connect(filepath.Join(dataDir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	if _, err := pool.Exec("CREATE TABLE IF NOT EXISTS names (name TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if _, err := pool.Exec("INSERT INTO names (name) VALUES (?)", n); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

func readNames(t *testing.T, path string) []string {
	t.Helper()
	pool, err := db.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := pool.Query("SELECT name FROM names ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

// backUp replicates dataDir/relPath holding names to cfg's replica.
func backUp(t *testing.T, cfg config.BackupConfig, dataDir, relPath string, names ...string) {
	t.Helper()
	ctx := context.Background()
	pool := writeNames(t, dataDir, relPath, names...)

	r, err := New(ctx, cfg, dataDir, discardLogger)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Add(relPath); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.SyncDB(ctx, filepath.Join(dataDir, relPath), true); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReplicateAndRestore(t *testing.T) {
	ctx := context.Background()
	cfg := fileConfig(t)
	relPath := filepath.Join("acme", "db.sqlite")
	backUp(t, cfg, t.TempDir(), relPath, "alice", "bob")

	restoreDir := t.TempDir()
	if err := Restore(ctx, cfg, io.Discard, restoreDir, relPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got, want := readNames(t, filepath.Join(restoreDir, relPath)), []string{"alice", "bob"}; !slices.Equal(got, want) {
		t.Errorf("restored names = %v, want %v", got, want)
	}
}

func TestRestore(t *testing.T) {
	ctx := context.Background()
	backedUp := fileConfig(t)
	backUp(t, backedUp, t.TempDir(), "ownscale.sqlite", "from-backup")

	cases := []struct {
		name  string
		cfg   config.BackupConfig
		local []string // names in the local database; nil if it's missing
		want  []string // names afterwards; nil if the database is missing
	}{
		{name: "missing, restored", cfg: backedUp, want: []string{"from-backup"}},
		{name: "present, left alone", cfg: backedUp, local: []string{"local"}, want: []string{"local"}},
		{name: "no backup", cfg: fileConfig(t)},
		{name: "disabled", cfg: config.BackupConfig{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, "ownscale.sqlite")
			if tc.local != nil {
				writeNames(t, dataDir, "ownscale.sqlite", tc.local...).Close()
			}

			if err := Restore(ctx, tc.cfg, io.Discard, dataDir, "ownscale.sqlite"); err != nil {
				t.Fatalf("Restore: %v", err)
			}
			exists, err := fileExists(path)
			if err != nil {
				t.Fatal(err)
			}
			if exists != (tc.want != nil) {
				t.Fatalf("database exists = %v, want %v", exists, tc.want != nil)
			}
			if exists {
				if got := readNames(t, path); !slices.Equal(got, tc.want) {
					t.Errorf("names = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestVerify(t *testing.T) {
	ctx := context.Background()
	backedUp := fileConfig(t)
	backUp(t, backedUp, t.TempDir(), "ownscale.sqlite", "from-backup")

	cases := []struct {
		name    string
		cfg     config.BackupConfig
		local   bool
		wantErr string
	}{
		{name: "missing with backup", cfg: backedUp, wantErr: "osa restore"},
		{name: "present with backup", cfg: backedUp, local: true},
		{name: "missing without backup", cfg: fileConfig(t)},
		{name: "present without backup", cfg: fileConfig(t), local: true},
		{name: "disabled", cfg: config.BackupConfig{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			if tc.local {
				writeNames(t, dataDir, "ownscale.sqlite").Close()
			}
			err := Verify(ctx, tc.cfg, io.Discard, dataDir, "ownscale.sqlite")
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("Verify: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Verify error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestDisabledLeavesNoTrace(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	pool := writeNames(t, dataDir, "ownscale.sqlite", "alice")

	r, err := New(ctx, config.BackupConfig{}, dataDir, discardLogger)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Add("ownscale.sqlite"); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-litestream") {
			t.Errorf("found Litestream state %q with backups disabled", e.Name())
		}
	}
}

func TestNewClient(t *testing.T) {
	cases := []struct {
		name    string
		cfg     config.BackupConfig
		relPath string
		check   func(t *testing.T, c any)
	}{
		{
			name: "s3 with endpoint and credentials",
			cfg: config.BackupConfig{
				ReplicaURL:      "s3://bucket/ownscale",
				AccessKeyID:     "key",
				SecretAccessKey: "secret",
				Region:          "eu-central-1",
				Endpoint:        "https://s3.example.com",
			},
			relPath: "acme/db.sqlite",
			check: func(t *testing.T, c any) {
				s3c := c.(*s3.ReplicaClient)
				if s3c.Bucket != "bucket" || s3c.Path != "ownscale/acme/db.sqlite" {
					t.Errorf("bucket/path = %q/%q", s3c.Bucket, s3c.Path)
				}
				if s3c.Region != "eu-central-1" || s3c.Endpoint != "https://s3.example.com" || !s3c.ForcePathStyle {
					t.Errorf("region/endpoint/path style = %q/%q/%v", s3c.Region, s3c.Endpoint, s3c.ForcePathStyle)
				}
				if s3c.AccessKeyID != "key" || s3c.SecretAccessKey != "secret" {
					t.Errorf("credentials = %q/%q", s3c.AccessKeyID, s3c.SecretAccessKey)
				}
			},
		},
		{
			name:    "abs with account key",
			cfg:     config.BackupConfig{ReplicaURL: "abs://acct@container/path", AzureAccountKey: "azkey"},
			relPath: "ownscale.sqlite",
			check: func(t *testing.T, c any) {
				absc := c.(*abs.ReplicaClient)
				if absc.AccountName != "acct" || absc.Bucket != "container" || absc.Path != "path/ownscale.sqlite" || absc.AccountKey != "azkey" {
					t.Errorf("client = %+v", absc)
				}
			},
		},
		{
			name:    "file",
			cfg:     config.BackupConfig{ReplicaURL: "file:///var/backups"},
			relPath: "acme/db.sqlite",
			check: func(t *testing.T, c any) {
				if got := c.(*file.ReplicaClient).Path(); got != "/var/backups/acme/db.sqlite" {
					t.Errorf("path = %q", got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := newClient(tc.cfg, tc.relPath)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, c)
		})
	}
}
