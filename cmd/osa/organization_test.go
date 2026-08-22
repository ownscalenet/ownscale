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

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/google/uuid"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pressly/goose/v3"

	dbpkg "github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/dockerctl"
	"github.com/ownscalenet/ownscale/internal/dockerctl/dockerfake"
	"github.com/ownscalenet/ownscale/internal/orgs"
)

// newTestDB opens a temp-file SQLite database and applies the real embedded
// migrations via goose (the same ones used in production and by `osa
// migrate`), returning the pool and sqlcgen queries against it. Using a
// real database (rather than faking sqlcgen.Queries) means these tests
// exercise real SQL behavior (uniqueness constraints, transaction rollback,
// etc.), matching the direction to fake only the Docker dependency.
func newTestDB(t *testing.T) (*sql.DB, *dbpkg.TxQueries) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	pool, err := dbpkg.Connect(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

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
	return pool, dbpkg.New(pool, logger)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// insertOrg inserts an organization row directly via queries, mirroring
// what orgs.Create does before it hands off to Docker, and returns it.
func insertOrg(t *testing.T, queries *dbpkg.TxQueries, subdomain string) sqlcgen.Organization {
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

// newTestCtl builds a *dockerctl.Ctl backed by api (a
// *dockerfake.API, whose zero value already fakes every Docker call as a
// success), so the organization business logic can be exercised against
// dockerctl's real config/tar-building and container-naming logic without a
// Docker daemon. dataDir is a fresh temp directory since CreateOrganization
// creates real host directories under it.
func newTestCtl(t *testing.T, api *dockerfake.API) *dockerctl.Ctl {
	t.Helper()
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), discardLogger())
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	return ctl
}

func TestCreateOrganization(t *testing.T) {
	t.Run("invalid subdomain", func(t *testing.T) {
		_, queries := newTestDB(t)
		ctl := newTestCtl(t, &dockerfake.API{
			ContainerCreateFn: func(context.Context, *container.Config, *container.HostConfig, *network.NetworkingConfig, *ocispec.Platform, string) (container.CreateResponse, error) {
				t.Fatal("docker should not be invoked for an invalid subdomain")
				return container.CreateResponse{}, nil
			},
		})
		var out strings.Builder
		err := createOrganization(context.Background(), &out, orgs.NewService(queries, ctl, "example.com"), "ab")
		if err == nil {
			t.Fatal("expected an error for an invalid (too short) subdomain")
		}
	})

	t.Run("duplicate subdomain", func(t *testing.T) {
		_, queries := newTestDB(t)
		insertOrg(t, queries, "acme")

		ctl := newTestCtl(t, &dockerfake.API{})
		var out strings.Builder
		err := createOrganization(context.Background(), &out, orgs.NewService(queries, ctl, "example.com"), "acme")
		if err == nil {
			t.Fatal("expected an error creating a duplicate subdomain")
		}
	})

	t.Run("docker failure rolls back the DB row", func(t *testing.T) {
		_, queries := newTestDB(t)

		wantErr := errors.New("docker exploded")
		ctl := newTestCtl(t, &dockerfake.API{
			ContainerCreateFn: func(context.Context, *container.Config, *container.HostConfig, *network.NetworkingConfig, *ocispec.Platform, string) (container.CreateResponse, error) {
				return container.CreateResponse{}, wantErr
			},
		})
		var out strings.Builder
		err := createOrganization(context.Background(), &out, orgs.NewService(queries, ctl, "example.com"), "acme")
		if err == nil || !strings.Contains(err.Error(), "docker exploded") {
			t.Fatalf("got err %v, want it to wrap %v", err, wantErr)
		}

		if _, err := queries.GetOrganizationBySubdomain(context.Background(), "acme"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected no organization row after rollback, got err %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		_, queries := newTestDB(t)

		ctl := newTestCtl(t, &dockerfake.API{})
		var out strings.Builder
		if err := createOrganization(context.Background(), &out, orgs.NewService(queries, ctl, "example.com"), "acme"); err != nil {
			t.Fatalf("createOrganization: %v", err)
		}

		if !strings.Contains(out.String(), "created organization acme.example.com") {
			t.Errorf("output = %q, want it to mention the created organization", out.String())
		}
		org, err := queries.GetOrganizationBySubdomain(context.Background(), "acme")
		if err != nil {
			t.Fatalf("GetOrganizationBySubdomain: %v", err)
		}
		wantContainerID := "fake-" + dockerctl.HSContainerName("acme")
		if org.ContainerID != wantContainerID {
			t.Errorf("ContainerID = %q, want %q", org.ContainerID, wantContainerID)
		}
	})
}

func TestDeleteOrganization(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		err := deleteOrganization(context.Background(), &out, queries, newTestCtl(t, &dockerfake.API{}), "missing")
		if err == nil || !strings.Contains(err.Error(), "no organization with subdomain") {
			t.Fatalf("got err %v, want a not-found error", err)
		}
	})

	t.Run("docker failure leaves the DB row intact", func(t *testing.T) {
		_, queries := newTestDB(t)
		insertOrg(t, queries, "acme")

		wantErr := errors.New("cannot remove container")
		ctl := newTestCtl(t, &dockerfake.API{
			ContainerRemoveFn: func(context.Context, string, container.RemoveOptions) error { return wantErr },
		})
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

		var removed []string
		ctl := newTestCtl(t, &dockerfake.API{
			ContainerRemoveFn: func(_ context.Context, containerID string, _ container.RemoveOptions) error {
				removed = append(removed, containerID)
				return nil
			},
		})
		var out strings.Builder
		if err := deleteOrganization(context.Background(), &out, queries, ctl, "acme"); err != nil {
			t.Fatalf("deleteOrganization: %v", err)
		}
		if !strings.Contains(out.String(), "deleted organization acme") {
			t.Errorf("output = %q, want a deletion confirmation", out.String())
		}
		wantRemoved := []string{dockerctl.HSContainerName("acme"), dockerctl.HPContainerName("acme")}
		if len(removed) != len(wantRemoved) || removed[0] != wantRemoved[0] || removed[1] != wantRemoved[1] {
			t.Errorf("removed containers = %v, want %v", removed, wantRemoved)
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
		if err := listOrganizations(context.Background(), &out, queries, newTestCtl(t, &dockerfake.API{})); err != nil {
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

		ctl := newTestCtl(t, &dockerfake.API{
			ContainerInspectFn: func(_ context.Context, containerID string) (container.InspectResponse, error) {
				if containerID == dockerctl.HSContainerName("beta") {
					return container.InspectResponse{}, errors.New("no such container")
				}
				return container.InspectResponse{
					ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "running"}},
				}, nil
			},
		})
		var out strings.Builder
		if err := listOrganizations(context.Background(), &out, queries, ctl); err != nil {
			t.Fatalf("listOrganizations: %v", err)
		}
		if !strings.Contains(out.String(), "acme") || !strings.Contains(out.String(), "running") {
			t.Errorf("output missing acme/running row: %q", out.String())
		}
		if !strings.Contains(out.String(), "error:") || !strings.Contains(out.String(), "no such container") {
			t.Errorf("output missing beta error row: %q", out.String())
		}
	})
}

func TestStartOrganizations(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		if err := startOrganizations(context.Background(), &out, discardLogger(), queries, newTestCtl(t, &dockerfake.API{})); err != nil {
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

		ctl := newTestCtl(t, &dockerfake.API{
			ContainerStartFn: func(_ context.Context, containerID string, _ container.StartOptions) error {
				if containerID == dockerctl.HSContainerName("beta") {
					return errors.New("container missing")
				}
				return nil
			},
		})
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
		var stopCalled bool
		var gotTimeout *int
		ctl := newTestCtl(t, &dockerfake.API{
			ContainerListFn: func(context.Context, container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{{ID: "abc", Names: []string{"/" + dockerctl.HSContainerName("acme")}}}, nil
			},
			ContainerStopFn: func(_ context.Context, _ string, options container.StopOptions) error {
				stopCalled = true
				gotTimeout = options.Timeout
				return nil
			},
		})
		var out strings.Builder
		if err := stopOrganizations(context.Background(), &out, ctl, 5*time.Second); err != nil {
			t.Fatalf("stopOrganizations: %v", err)
		}
		if !stopCalled || gotTimeout == nil || *gotTimeout != 5 {
			t.Errorf("stopCalled=%v gotTimeout=%v, want called with 5s", stopCalled, gotTimeout)
		}
		if !strings.Contains(out.String(), "stopped all organization containers") {
			t.Errorf("output = %q, want a stop confirmation", out.String())
		}
	})

	t.Run("wraps a stop error", func(t *testing.T) {
		wantErr := errors.New("stop exploded")
		ctl := newTestCtl(t, &dockerfake.API{
			ContainerListFn: func(context.Context, container.ListOptions) ([]container.Summary, error) {
				return []container.Summary{{ID: "abc", Names: []string{"/" + dockerctl.HSContainerName("acme")}}}, nil
			},
			ContainerStopFn: func(context.Context, string, container.StopOptions) error { return wantErr },
		})
		var out strings.Builder
		err := stopOrganizations(context.Background(), &out, ctl, time.Second)
		if err == nil || !strings.Contains(err.Error(), "stop exploded") {
			t.Fatalf("got err %v, want it to wrap %v", err, wantErr)
		}
	})
}
