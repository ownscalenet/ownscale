package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
	"github.com/ownscalenet/ownscale/headscaleclient"
	"github.com/ownscalenet/ownscale/orgs"
)

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

// insertOrg inserts an organization row directly via queries, mirroring
// what orgs.Create does before it hands off to Docker, and returns it.
func insertOrg(t *testing.T, queries *dbpkg.TxQueries, subdomain string) sqlcgen.Organization {
	t.Helper()
	org, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: subdomain,
		Status:    orgs.StatusActive,
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
// creates real host directories under it. It installs a fake MintClientFn
// so CreateOrganization's default-policy seeding step doesn't attempt a
// real network round trip to a container hostname that doesn't actually
// exist under dockerfake.
func newTestCtl(t *testing.T, api *dockerfake.API) *dockerctl.Ctl {
	t.Helper()
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	ctl.MintClientFn = func(ctx context.Context, subdomain string) (*headscaleclient.Client, error) {
		return newFakePolicyClient(t), nil
	}
	return ctl
}

// newFakePolicyClient returns a headscaleclient.Client pointed at an
// httptest.Server implementing headscale's policy endpoint (GET/PUT
// /api/v1/policy), accepting any policy content unconditionally.
func newFakePolicyClient(t *testing.T) *headscaleclient.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/policy", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"policy":""}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	return headscaleclient.New(host, "test-api-key")
}

func TestBaseDomain(t *testing.T) {
	t.Run("empty rejected", func(t *testing.T) {
		if _, err := baseDomain(&config.Config{}); err == nil {
			t.Fatal("baseDomain: want error for empty base_domain, got nil")
		}
	})

	t.Run("localhost rejected", func(t *testing.T) {
		for _, v := range []string{"localhost", "LOCALHOST", "LocalHost"} {
			_, err := baseDomain(&config.Config{BaseDomain: v})
			if err == nil {
				t.Fatalf("baseDomain: want error for base_domain %q, got nil", v)
			}
			if !strings.Contains(err.Error(), "localhost") {
				t.Errorf("baseDomain error = %q, want it to mention %q", err.Error(), "localhost")
			}
		}
	})

	t.Run("valid domain passes", func(t *testing.T) {
		got, err := baseDomain(&config.Config{BaseDomain: "example.com"})
		if err != nil {
			t.Fatalf("baseDomain: %v", err)
		}
		if got != "example.com" {
			t.Errorf("baseDomain = %q, want %q", got, "example.com")
		}
	})
}

func TestDeleteOrganization(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		err := deleteOrganization(t.Context(), &out, queries, newTestCtl(t, &dockerfake.API{}), "missing")
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
		err := deleteOrganization(t.Context(), &out, queries, ctl, "acme")
		if err == nil || !strings.Contains(err.Error(), "cannot remove container") {
			t.Fatalf("got err %v, want it to wrap %v", err, wantErr)
		}
		if _, err := queries.GetOrganizationBySubdomain(t.Context(), "acme"); err != nil {
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
		if err := deleteOrganization(t.Context(), &out, queries, ctl, "acme"); err != nil {
			t.Fatalf("deleteOrganization: %v", err)
		}
		if !strings.Contains(out.String(), "deleted organization acme") {
			t.Errorf("output = %q, want a deletion confirmation", out.String())
		}
		wantRemoved := []string{dockerctl.HSContainerName("acme")}
		if len(removed) != len(wantRemoved) || removed[0] != wantRemoved[0] {
			t.Errorf("removed containers = %v, want %v", removed, wantRemoved)
		}
		if _, err := queries.GetOrganizationBySubdomain(t.Context(), "acme"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected organization row to be gone, got err %v", err)
		}
	})
}

func TestListOrganizations(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, queries := newTestDB(t)
		var out strings.Builder
		if err := listOrganizations(t.Context(), &out, queries, newTestCtl(t, &dockerfake.API{})); err != nil {
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
		if err := listOrganizations(t.Context(), &out, queries, ctl); err != nil {
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
		if err := startOrganizations(t.Context(), &out, discardLogger(), queries, newTestCtl(t, &dockerfake.API{}), "example.com"); err != nil {
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
		err := startOrganizations(t.Context(), &out, discardLogger(), queries, ctl, "example.com")
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
		if err := stopOrganizations(t.Context(), &out, ctl, 5*time.Second); err != nil {
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
		err := stopOrganizations(t.Context(), &out, ctl, time.Second)
		if err == nil || !strings.Contains(err.Error(), "stop exploded") {
			t.Fatalf("got err %v, want it to wrap %v", err, wantErr)
		}
	})
}
