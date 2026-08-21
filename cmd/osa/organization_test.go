package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"

	dbpkg "github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
)

// newTestDB opens a temp-file SQLite database and applies the real embedded
// migrations via goose (the same ones used in production and by `osa
// migrate`), returning the pool and sqlcgen queries against it. Using a
// real database (rather than faking sqlcgen.Queries) means these tests
// exercise real SQL behavior (uniqueness constraints, transaction rollback,
// etc.), matching the direction to fake only the Docker dependency.
func newTestDB(t *testing.T) (*sql.DB, *sqlcgen.Queries) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	pool, err := dbpkg.Connect(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	sub, err := fs.Sub(dbpkg.MigrationsFS, "migrations")
	if err != nil {
		t.Fatalf("migrations sub fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, pool, sub)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return pool, sqlcgen.New(dbpkg.NewLoggingDBTX(pool, logger))
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// insertOrg inserts an organization row directly via queries, mirroring
// what orgs.Create does before it hands off to Docker, and returns it.
func insertOrg(t *testing.T, queries *sqlcgen.Queries, subdomain string) sqlcgen.Organization {
	t.Helper()
	org, err := queries.CreateOrganization(context.Background(), sqlcgen.CreateOrganizationParams{
		ID:        uuid.Must(uuid.NewV7()).String(),
		Subdomain: subdomain,
	})
	if err != nil {
		t.Fatalf("insertOrg(%q): %v", subdomain, err)
	}
	return org
}

// fakeCtl is a hand-written dockerctl.Controller fake: no Docker daemon is
// required to exercise the organization business logic, only the
// interactions it has with the Controller interface.
type fakeCtl struct {
	createFn func(ctx context.Context, queries *sqlcgen.Queries, orgID, subdomain, baseDomain string) (string, error)
	removeFn func(ctx context.Context, subdomain string) error
	startFn  func(ctx context.Context, subdomain string) error
	stateFn  func(ctx context.Context, subdomain string) (string, error)
	stopFn   func(ctx context.Context, timeout time.Duration) error

	removed     []string
	started     []string
	stopTimeout time.Duration
	stopCalled  bool
}

func (f *fakeCtl) CreateOrganization(ctx context.Context, queries *sqlcgen.Queries, orgID, subdomain, baseDomain string) (string, error) {
	if f.createFn != nil {
		return f.createFn(ctx, queries, orgID, subdomain, baseDomain)
	}
	return "container-" + subdomain, nil
}

func (f *fakeCtl) RemoveOrganization(ctx context.Context, subdomain string) error {
	f.removed = append(f.removed, subdomain)
	if f.removeFn != nil {
		return f.removeFn(ctx, subdomain)
	}
	return nil
}

func (f *fakeCtl) StartOrganization(ctx context.Context, subdomain string) error {
	f.started = append(f.started, subdomain)
	if f.startFn != nil {
		return f.startFn(ctx, subdomain)
	}
	return nil
}

func (f *fakeCtl) ContainerState(ctx context.Context, subdomain string) (string, error) {
	if f.stateFn != nil {
		return f.stateFn(ctx, subdomain)
	}
	return "running", nil
}

func (f *fakeCtl) StopAll(ctx context.Context, timeout time.Duration) error {
	f.stopCalled = true
	f.stopTimeout = timeout
	if f.stopFn != nil {
		return f.stopFn(ctx, timeout)
	}
	return nil
}

func (f *fakeCtl) Close() error { return nil }

func TestCreateOrganization(t *testing.T) {
	t.Run("invalid subdomain", func(t *testing.T) {
		pool, _ := newTestDB(t)
		ctl := &fakeCtl{createFn: func(context.Context, *sqlcgen.Queries, string, string, string) (string, error) {
			t.Fatal("docker should not be invoked for an invalid subdomain")
			return "", nil
		}}
		var out strings.Builder
		err := createOrganization(context.Background(), &out, pool, ctl, discardLogger(), "example.com", "ab")
		if err == nil {
			t.Fatal("expected an error for an invalid (too short) subdomain")
		}
	})

	t.Run("duplicate subdomain", func(t *testing.T) {
		pool, queries := newTestDB(t)
		insertOrg(t, queries, "acme")

		ctl := &fakeCtl{}
		var out strings.Builder
		err := createOrganization(context.Background(), &out, pool, ctl, discardLogger(), "example.com", "acme")
		if err == nil {
			t.Fatal("expected an error creating a duplicate subdomain")
		}
	})

	t.Run("docker failure rolls back the DB row", func(t *testing.T) {
		pool, queries := newTestDB(t)

		wantErr := errors.New("docker exploded")
		ctl := &fakeCtl{createFn: func(context.Context, *sqlcgen.Queries, string, string, string) (string, error) {
			return "", wantErr
		}}
		var out strings.Builder
		err := createOrganization(context.Background(), &out, pool, ctl, discardLogger(), "example.com", "acme")
		if err == nil || !strings.Contains(err.Error(), "docker exploded") {
			t.Fatalf("got err %v, want it to wrap %v", err, wantErr)
		}

		if _, err := queries.GetOrganizationBySubdomain(context.Background(), "acme"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected no organization row after rollback, got err %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		pool, queries := newTestDB(t)

		ctl := &fakeCtl{}
		var out strings.Builder
		if err := createOrganization(context.Background(), &out, pool, ctl, discardLogger(), "example.com", "acme"); err != nil {
			t.Fatalf("createOrganization: %v", err)
		}

		if !strings.Contains(out.String(), "created organization acme.example.com") {
			t.Errorf("output = %q, want it to mention the created organization", out.String())
		}
		org, err := queries.GetOrganizationBySubdomain(context.Background(), "acme")
		if err != nil {
			t.Fatalf("GetOrganizationBySubdomain: %v", err)
		}
		if org.ContainerID != "container-acme" {
			t.Errorf("ContainerID = %q, want %q", org.ContainerID, "container-acme")
		}
	})
}

func TestDeleteOrganization(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		err := deleteOrganization(context.Background(), &out, queries, &fakeCtl{}, "missing")
		if err == nil || !strings.Contains(err.Error(), "no organization with subdomain") {
			t.Fatalf("got err %v, want a not-found error", err)
		}
	})

	t.Run("docker failure leaves the DB row intact", func(t *testing.T) {
		_, queries := newTestDB(t)
		insertOrg(t, queries, "acme")

		wantErr := errors.New("cannot remove container")
		ctl := &fakeCtl{removeFn: func(context.Context, string) error { return wantErr }}
		var out strings.Builder
		err := deleteOrganization(context.Background(), &out, queries, ctl, "acme")
		if err == nil || !strings.Contains(err.Error(), "cannot remove container") {
			t.Fatalf("got err %v, want it to wrap %v", err, wantErr)
		}
		if _, err := queries.GetOrganizationBySubdomain(context.Background(), "acme"); err != nil {
			t.Fatalf("expected organization row to still exist, got err %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		_, queries := newTestDB(t)
		insertOrg(t, queries, "acme")

		ctl := &fakeCtl{}
		var out strings.Builder
		if err := deleteOrganization(context.Background(), &out, queries, ctl, "acme"); err != nil {
			t.Fatalf("deleteOrganization: %v", err)
		}
		if !strings.Contains(out.String(), "deleted organization acme") {
			t.Errorf("output = %q, want a deletion confirmation", out.String())
		}
		if len(ctl.removed) != 1 || ctl.removed[0] != "acme" {
			t.Errorf("removed = %v, want [acme]", ctl.removed)
		}
		if _, err := queries.GetOrganizationBySubdomain(context.Background(), "acme"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected organization row to be gone, got err %v", err)
		}
	})
}

func TestListOrganizations(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		if err := listOrganizations(context.Background(), &out, queries, &fakeCtl{}); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		if strings.TrimSpace(out.String()) != "no organizations" {
			t.Errorf("output = %q, want %q", out.String(), "no organizations")
		}
	})

	t.Run("reports a container state error inline", func(t *testing.T) {
		_, queries := newTestDB(t)
		insertOrg(t, queries, "acme")
		insertOrg(t, queries, "beta")

		ctl := &fakeCtl{stateFn: func(_ context.Context, subdomain string) (string, error) {
			if subdomain == "beta" {
				return "", errors.New("no such container")
			}
			return "running", nil
		}}
		var out strings.Builder
		if err := listOrganizations(context.Background(), &out, queries, ctl); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		if !strings.Contains(out.String(), "acme") || !strings.Contains(out.String(), "running") {
			t.Errorf("output missing acme/running row: %q", out.String())
		}
		if !strings.Contains(out.String(), "error: no such container") {
			t.Errorf("output missing beta error row: %q", out.String())
		}
	})
}

func TestStartOrganizations(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		if err := startOrganizations(context.Background(), &out, discardLogger(), queries, &fakeCtl{}); err != nil {
			t.Fatalf("startOrganizations: %v", err)
		}
		if strings.TrimSpace(out.String()) != "no organizations" {
			t.Errorf("output = %q, want %q", out.String(), "no organizations")
		}
	})

	t.Run("partial failure is reported and counted", func(t *testing.T) {
		_, queries := newTestDB(t)
		insertOrg(t, queries, "acme")
		insertOrg(t, queries, "beta")

		ctl := &fakeCtl{startFn: func(_ context.Context, subdomain string) error {
			if subdomain == "beta" {
				return errors.New("container missing")
			}
			return nil
		}}
		var out strings.Builder
		err := startOrganizations(context.Background(), &out, discardLogger(), queries, ctl)
		if err == nil || !strings.Contains(err.Error(), "failed to start 1/2 organizations") {
			t.Fatalf("got err %v, want a 1/2 failure count", err)
		}
		if !strings.Contains(out.String(), "started organization acme") {
			t.Errorf("output = %q, want it to report acme starting", out.String())
		}
	})
}

func TestStopOrganizations(t *testing.T) {
	t.Run("passes the timeout through", func(t *testing.T) {
		ctl := &fakeCtl{}
		var out strings.Builder
		if err := stopOrganizations(context.Background(), &out, ctl, 5*time.Second); err != nil {
			t.Fatalf("stopOrganizations: %v", err)
		}
		if !ctl.stopCalled || ctl.stopTimeout != 5*time.Second {
			t.Errorf("stopCalled=%v stopTimeout=%v, want called with 5s", ctl.stopCalled, ctl.stopTimeout)
		}
		if !strings.Contains(out.String(), "stopped all organization containers") {
			t.Errorf("output = %q, want a stop confirmation", out.String())
		}
	})

	t.Run("wraps a stop error", func(t *testing.T) {
		wantErr := errors.New("stop exploded")
		ctl := &fakeCtl{stopFn: func(context.Context, time.Duration) error { return wantErr }}
		var out strings.Builder
		err := stopOrganizations(context.Background(), &out, ctl, time.Second)
		if err == nil || !strings.Contains(err.Error(), "stop exploded") {
			t.Fatalf("got err %v, want it to wrap %v", err, wantErr)
		}
	})
}
