package main

import (
	"database/sql"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/xid"

	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/orgs"
)

// port is required and unique so use atomic to prevent race conditions
// in concurrent tests.
var nextTestPort atomic.Int64

func testPort() int64 { return 10_000 + nextTestPort.Add(1) - 1 }

// newTestDB returns a pool and queries backed by a copy of the shared,
// already migrated dbtest template (see db/dbtest), avoiding a full goose
// run per test. Using a real database (rather than faking sqlcgen.Queries)
// means these tests exercise real SQL behavior (uniqueness constraints,
// transaction rollback, etc.), matching the direction to fake only the
// Docker dependency.
func newTestDB(t *testing.T) (*sql.DB, *dbpkg.TxQueries) {
	t.Helper()
	return dbtest.New(t)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func insertOrg(t *testing.T, queries *dbpkg.TxQueries, subdomain string) sqlcgen.Organization {
	t.Helper()
	org, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: subdomain,
		Status:    orgs.StatusActive,
		Port:      testPort(),
	})
	if err != nil {
		t.Fatalf("insertOrg(%q): %v", subdomain, err)
	}
	return org
}

func TestListOrganizations(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		if err := listOrganizations(t.Context(), &out, queries); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		if strings.TrimSpace(out.String()) != "no organizations" {
			t.Errorf("output = %q, want %q", out.String(), "no organizations")
		}
	})

	t.Run("lists organizations", func(t *testing.T) {
		_, queries := newTestDB(t)
		insertOrg(t, queries, "acme")
		insertOrg(t, queries, "beta")

		var out strings.Builder
		if err := listOrganizations(t.Context(), &out, queries); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		for _, want := range []string{"acme", "beta", orgs.StatusActive} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output missing %q: %q", want, out.String())
			}
		}
	})
}
