package handlers

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
	"github.com/ownscalenet/ownscale/headscaleclient"

	"github.com/rs/xid"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestQueries opens a temp-file SQLite database and applies the real
// embedded migrations via goose, mirroring cmd/osa's newTestDB helper.
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

	return dbpkg.New(pool, discardLogger())
}

// newTestCtl builds a *dockerctl.Ctl backed by api (whose zero value already
// fakes every Docker call as a success), so organization creation can be
// exercised without a real Docker daemon.
func newTestCtl(t *testing.T, api *dockerfake.API) *dockerctl.Ctl {
	t.Helper()
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	return ctl
}

// noopDockerAPI is a *dockerfake.API whose zero value already fakes every
// Docker call as a success (containers "exist" and run immediately).
func noopDockerAPI() *dockerfake.API {
	return &dockerfake.API{}
}

const testBaseDomain = "example.com"

// newTestHandlers builds a *Handlers backed by a real, freshly-migrated
// SQLite database and (if withOrgs is true) a dockerfake-backed
// dockerctl.Ctl, so organization provisioning is fully configured.
func newTestHandlers(t *testing.T, withOrgs bool) (*Handlers, *dbpkg.TxQueries) {
	t.Helper()
	queries := newTestQueries(t)

	var ctl *dockerctl.Ctl
	baseDomain := ""
	if withOrgs {
		ctl = newTestCtl(t, noopDockerAPI())
		baseDomain = testBaseDomain
	}
	return New(queries, ctl, baseDomain, nil, discardLogger(), config.DerpConfig{}), queries
}

// withActiveOrg attaches (caller, org) to req's context, as RequireActiveOrg
// would for an authenticated request against an active organization.
func withActiveOrg(req *http.Request, caller sqlcgen.User, org sqlcgen.Organization) *http.Request {
	ctx := context.WithValue(req.Context(), activeOrgContextKey, activeOrgContext{caller: &caller, org: org})
	return req.WithContext(ctx)
}

// withMachinesClient attaches client to req's context, as
// RequireMachinesClient would for an authenticated request whose
// organization's headscale is reachable.
func withMachinesClient(req *http.Request, client *headscaleclient.Client) *http.Request {
	ctx := context.WithValue(req.Context(), machinesClientContextKey, client)
	return req.WithContext(ctx)
}

// insertUser inserts a user row directly and returns it.
func insertUser(t *testing.T, queries *dbpkg.TxQueries) sqlcgen.User {
	t.Helper()
	id := xid.New().String()
	user, err := queries.CreateUser(t.Context(), sqlcgen.CreateUserParams{
		ID:             id,
		Provider:       "oidc",
		ProviderUserID: id,
		Email:          id + "@example.com",
		Name:           "Test User",
	})
	if err != nil {
		t.Fatalf("insertUser: %v", err)
	}
	return user
}

func setupSessions() {
	auth.Setup("test-session-secret-at-least-32-bytes-long")
}

// authenticatedRequest returns req with a session cookie for user attached,
// via auth.StoreUserSession, so it will pass auth.RequireAuthAPI.
func authenticatedRequest(t *testing.T, req *http.Request, userID string) *http.Request {
	t.Helper()
	setupSessions()
	rec := httptest.NewRecorder()
	if err := auth.StoreUserSession(rec, httptest.NewRequest(http.MethodGet, "/", nil), userID); err != nil {
		t.Fatalf("StoreUserSession: %v", err)
	}
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}
