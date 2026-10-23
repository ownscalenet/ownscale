package main

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/ownscalenet/ownscale/db"
)

// testProvider returns a goose provider, and the pool it wraps, over a
// fresh, unmigrated SQLite database in t.TempDir(), mirroring
// newGooseProvider without going through env-based config.
func testProvider(t *testing.T) (*goose.Provider, *sql.DB) {
	t.Helper()
	pool, err := db.Connect(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	sub, err := fs.Sub(db.MigrationsFS, "migrations")
	if err != nil {
		t.Fatalf("migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, pool, sub)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	return provider, pool
}

// appTables returns the names of non-sqlite, non-goose tables present in
// pool.
func appTables(t *testing.T, pool *sql.DB) []string {
	t.Helper()
	rows, err := pool.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table'
		 AND name NOT LIKE 'sqlite_%' AND name != 'goose_db_version'`,
	)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	return names
}

// TestMigrateUpDownStatus walks every embedded migration up one at a time
// with migrateUp, then back down one at a time with migrateDown, checking
// migrateStatus and the resulting schema at each step. It exercises the
// "osa migrate" subcommands directly, since make e2e / unit tests otherwise
// only ever apply the full set of migrations in one shot (db/dbtest).
func TestMigrateUpDownStatus(t *testing.T) {
	ctx := context.Background()
	provider, pool := testProvider(t)
	sources := provider.ListSources()
	if len(sources) == 0 {
		t.Fatal("no embedded migrations found")
	}

	if err := migrateStatus(ctx, provider); err != nil {
		t.Fatalf("migrateStatus before any migration: %v", err)
	}
	if tables := appTables(t, pool); len(tables) != 0 {
		t.Fatalf("app tables before any migration: %v", tables)
	}

	// Apply every migration one at a time.
	for i := range sources {
		if err := migrateUp(ctx, provider); err != nil {
			t.Fatalf("migrateUp step %d: %v", i, err)
		}
	}
	statuses, err := provider.Status(ctx)
	if err != nil {
		t.Fatalf("provider.Status after full up: %v", err)
	}
	for _, s := range statuses {
		if s.State != goose.StateApplied {
			t.Fatalf("migration %s not applied after migrateUp loop", s.Source.Path)
		}
	}
	if err := migrateStatus(ctx, provider); err != nil {
		t.Fatalf("migrateStatus after full up: %v", err)
	}
	// One more call with nothing pending must be a no-op, not an error.
	if err := migrateUp(ctx, provider); err != nil {
		t.Fatalf("migrateUp with nothing pending: %v", err)
	}
	if tables := appTables(t, pool); len(tables) == 0 {
		t.Fatal("expected app tables to exist after migrating up")
	}

	// Revert every migration one at a time, back to empty.
	for i := range slices.Backward(sources) {
		if err := migrateDown(ctx, provider); err != nil {
			t.Fatalf("migrateDown step %d: %v", i, err)
		}
	}
	if tables := appTables(t, pool); len(tables) != 0 {
		t.Fatalf("app tables remain after reverting every migration: %v", tables)
	}
	if err := migrateStatus(ctx, provider); err != nil {
		t.Fatalf("migrateStatus after full down: %v", err)
	}

	// migrateDown with nothing applied must return an error, same as the
	// underlying goose.ErrNoNextVersion / goose.ErrNoCurrentVersion case.
	if err := migrateDown(ctx, provider); err == nil {
		t.Fatal("migrateDown with nothing applied: expected an error, got nil")
	}
}
