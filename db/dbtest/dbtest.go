// Package dbtest gives tests a fast, already-migrated SQLite database.
// Goose migrations run once per test binary into a template file; each
// call to New copies that template instead of re-running every migration.
package dbtest

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/rs/xid"

	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

var (
	templateOnce sync.Once
	templatePath string
	templateErr  error
)

// buildTemplate migrates a scratch SQLite file once and returns its path.
func buildTemplate() (string, error) {
	templateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ownscale-dbtest-template")
		if err != nil {
			templateErr = err
			return
		}
		path := filepath.Join(dir, "template.sqlite")
		pool, err := dbpkg.Connect(path)
		if err != nil {
			templateErr = err
			return
		}
		defer pool.Close()

		sub, err := fs.Sub(dbpkg.MigrationsFS, "migrations")
		if err != nil {
			templateErr = err
			return
		}
		provider, err := goose.NewProvider(goose.DialectSQLite3, pool, sub)
		if err != nil {
			templateErr = err
			return
		}
		if _, err := provider.Up(context.Background()); err != nil {
			templateErr = err
			return
		}
		// Fold the WAL back into the main file so a plain byte copy of
		// path below is a complete, self-contained database.
		if _, err := pool.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			templateErr = err
			return
		}
		templatePath = path
	})
	return templatePath, templateErr
}

// New returns a pool and queries backed by a freshly-copied, already
// migrated SQLite database in t.TempDir(), without re-running goose.
func New(t *testing.T) (*sql.DB, *dbpkg.TxQueries) {
	t.Helper()

	template, err := buildTemplate()
	if err != nil {
		t.Fatalf("build migration template: %v", err)
	}

	data, err := os.ReadFile(template)
	if err != nil {
		t.Fatalf("read migration template: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	if err := os.WriteFile(dbPath, data, 0o600); err != nil {
		t.Fatalf("copy migration template: %v", err)
	}

	pool, err := dbpkg.Connect(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	return pool, dbpkg.New(pool, slog.New(slog.DiscardHandler))
}

// CreateOrg inserts an active organization row for subdomain, without an
// OIDC client, stored config, or running headscale.
func CreateOrg(t testing.TB, q *sqlcgen.Queries, subdomain string) sqlcgen.Organization {
	t.Helper()
	org, err := q.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: subdomain,
		// orgs.StatusActive; orgs can't be imported here as its tests use dbtest.
		Status: "active",
	})
	if err != nil {
		t.Fatalf("CreateOrg(%q): %v", subdomain, err)
	}
	return org
}

// CreateUser inserts a user with role in organization orgID. Its ID,
// provider user ID and email are derived from a fresh xid.
func CreateUser(t testing.TB, q *sqlcgen.Queries, orgID, role string) sqlcgen.User {
	t.Helper()
	id := xid.New().String()
	user, err := q.CreateUser(t.Context(), sqlcgen.CreateUserParams{
		ID:             id,
		Provider:       "oidc",
		ProviderUserID: id,
		Email:          id + "@example.com",
		Name:           "Test User",
		OrganizationID: orgID,
		Role:           role,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}
