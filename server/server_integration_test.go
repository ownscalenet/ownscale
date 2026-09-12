//go:build integration

// server_integration_test.go is the "full stack" integration test that
// routes.go's doc comment anticipates: it wires the exact same
// server.NewMux/handlers.New stack as cmd/ownscale/main.go, but against a
// real, freshly-migrated SQLite database and a real Docker daemon (as
// opposed to the package's regular unit tests, which use an in-memory mux
// with no organization provisioning). Excluded from the default `go test
// ./...` run via the "integration" build tag; run explicitly with
// `go test -tags=integration ./...` (or `make test-integration`), which
// requires Docker running on the host and the headscale image
// already pulled.
package server_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/pressly/goose/v3"
	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/handlers"
	"github.com/ownscalenet/ownscale/headscaleclient"
	"github.com/ownscalenet/ownscale/orgs"
	"github.com/ownscalenet/ownscale/server"
)

// requireDocker skips the test if no Docker daemon is reachable, and
// returns a raw client for tests that need to inspect containers directly
// (e.g. to work around the test process not being able to resolve
// container names by DNS -- see the MintClientFn override below).
func requireDocker(t *testing.T) *client.Client {
	t.Helper()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("docker client unavailable: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}
	return cli
}

func newTestQueries(t *testing.T) *dbpkg.TxQueries {
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
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return dbpkg.New(pool, slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

// TestServerIntegration builds the exact same mux cmd/ownscale/main.go
// does, backed by a real database and a real Docker daemon, and drives it
// via httptest.NewServer over real HTTP (not the in-process
// httptest.NewRecorder used by the package's unit tests). It bypasses
// real Google OAuth (not practical to exercise in CI) by seeding a user
// and minting its session cookie directly via auth.StoreUserSession.
func TestServerIntegration(t *testing.T) {
	rawCli := requireDocker(t)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	queries := newTestQueries(t)

	ctl, err := dockerctl.New(t.TempDir(), "", logger)
	if err != nil {
		t.Fatalf("dockerctl.New: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	// The code relies on the os-net docker network to work (defined in docker-compose.yaml).
	// We don't have that network here in tests so instead of using container
	// name, we use container ID to make headscale requests.
	ctl.MintClientFn = func(ctx context.Context, subdomain string) (*headscaleclient.Client, error) {
		hsName := dockerctl.HSContainerName(subdomain)
		apiKey, err := ctl.MintHeadscaleAPIKey(ctx, hsName)
		if err != nil {
			return nil, err
		}
		info, err := rawCli.ContainerInspect(ctx, hsName)
		if err != nil {
			return nil, err
		}
		netInfo, ok := info.NetworkSettings.Networks[dockerctl.OrgNetworkName(subdomain)]
		if !ok || netInfo.IPAddress == "" {
			return nil, fmt.Errorf("container %q has no address on network %q", hsName, dockerctl.OrgNetworkName(subdomain))
		}
		return headscaleclient.New(netInfo.IPAddress+":8080", apiKey), nil
	}

	const baseDomain = "srvit.test"
	var subdomain string
	t.Cleanup(func() {
		if subdomain == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := ctl.RemoveOrganization(ctx, subdomain); err != nil {
			t.Logf("cleanup: RemoveOrganization(%q): %v", subdomain, err)
		}
	})

	h, err := handlers.New(queries, ctl, baseDomain, t.TempDir(), nil, logger, config.DerpConfig{}, config.ProvisionerConfig{})
	if err != nil {
		t.Fatalf("handlers.New: %v", err)
	}
	auth.Setup("test-session-secret-at-least-32-bytes-long")
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	mux := server.NewMux(h, requireAuthAPI, logger, http.NotFoundHandler(), nil)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// Seed a user and mint its session cookie directly, bypassing the
	// real OAuth redirect flow.
	id := xid.New().String()
	user, err := queries.CreateUser(t.Context(), sqlcgen.CreateUserParams{
		ID:             id,
		Provider:       "oidc",
		ProviderUserID: id,
		Email:          id + "@example.com",
		Name:           "Integration Test User",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	storeRec := httptest.NewRecorder()
	if err := auth.StoreUserSession(storeRec, httptest.NewRequest(http.MethodGet, "/", nil), user.ID); err != nil {
		t.Fatalf("StoreUserSession: %v", err)
	}
	sessionCookies := storeRec.Result().Cookies()

	authedRequest := func(t *testing.T, method, path string, body []byte) *http.Response {
		t.Helper()
		var reqBody io.Reader
		if body != nil {
			reqBody = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, ts.URL+path, reqBody)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		for _, c := range sessionCookies {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	t.Run("auto-provisioned organization pool assigns real containers", func(t *testing.T) {
		// Mirrors what handlers.AuthCallback does for a first-time user:
		// take a fully-provisioned organization out of the pool and assign
		// it. Built directly against orgs (rather than through h, whose
		// provisioner field is private) so this test can drive it exactly
		// like AuthCallback would.
		orgSvc := orgs.NewService(queries, ctl, baseDomain, "", config.DerpConfig{})
		provisioner := orgs.NewProvisioner(orgSvc, logger, 0, 1, 1)
		poolCtx, cancelPool := context.WithCancel(t.Context())
		defer cancelPool()
		go func() { provisioner.Run(poolCtx) }()

		takeCtx, cancelTake := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancelTake()
		org, err := provisioner.Take(takeCtx)
		if err != nil {
			t.Fatalf("provisioner.Take: %v", err)
		}
		subdomain = org.Subdomain

		if org.Status != orgs.StatusActive {
			t.Errorf("organization.status = %q, want %q", org.Status, orgs.StatusActive)
		}
		if err := queries.SetUserOrganizationAndRole(t.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
			OrganizationID: sql.NullString{String: org.ID, Valid: true},
			Role:           "owner",
			ID:             user.ID,
		}); err != nil {
			t.Fatalf("SetUserOrganizationAndRole: %v", err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		state, err := ctl.ContainerState(ctx, subdomain)
		if err != nil {
			t.Fatalf("ContainerState: %v", err)
		}
		if state != "running" {
			t.Errorf("ContainerState = %q, want %q", state, "running")
		}
	})

	t.Run("GET /api/session returns the seeded user", func(t *testing.T) {
		resp := authedRequest(t, http.MethodGet, "/api/session", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var body struct {
			User sqlcgen.User `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.User.ID != user.ID {
			t.Errorf("session user id = %q, want %q", body.User.ID, user.ID)
		}
	})

	t.Run("POST /api/logout clears the session", func(t *testing.T) {
		logoutResp := authedRequest(t, http.MethodPost, "/api/logout", nil)
		defer logoutResp.Body.Close()
		if logoutResp.StatusCode != http.StatusNoContent {
			t.Fatalf("logout status = %d, want %d", logoutResp.StatusCode, http.StatusNoContent)
		}

		// Replace the session cookies with whatever logout just set
		// (cleared/expired), then confirm a follow-up request 401s.
		sessionCookies = logoutResp.Cookies()
		afterResp := authedRequest(t, http.MethodGet, "/api/session", nil)
		defer afterResp.Body.Close()
		if afterResp.StatusCode != http.StatusUnauthorized {
			t.Errorf("session status after logout = %d, want %d", afterResp.StatusCode, http.StatusUnauthorized)
		}
	})
}
