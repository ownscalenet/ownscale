package handlers

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
	"github.com/ownscalenet/ownscale/headscaleclient"

	"github.com/rs/xid"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestQueries returns queries backed by a copy of the shared, already
// migrated dbtest template (see db/dbtest), avoiding a full goose run per
// test.
func newTestQueries(t *testing.T) *dbpkg.TxQueries {
	t.Helper()
	_, queries := dbtest.New(t)
	return queries
}

// newTestCtl builds a *dockerctl.Ctl backed by api (whose zero value already
// fakes every Docker call as a success), so organization creation can be
// exercised without a real Docker daemon. It installs a fake MintClientFn
// (backed by newFakePolicyServer) so CreateOrganization's default-policy
// seeding step doesn't attempt a real network round trip to a container
// hostname that doesn't actually exist under dockerfake.
func newTestCtl(t *testing.T, api *dockerfake.API) *dockerctl.Ctl {
	t.Helper()
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	ctl.MintClientFn = func(ctx context.Context, subdomain string) (*headscaleclient.Client, error) {
		client, _ := newFakePolicyServer(t, "")
		return client, nil
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
	return New(queries, ctl, baseDomain, t.TempDir(), nil, discardLogger(), config.DerpConfig{}, config.ProvisionerConfig{}), queries
}

// withActiveOrg attaches (caller, org) to req's context, as RequireActiveOrg
// would for an authenticated request against an active organization.
func withActiveOrg(req *http.Request, caller sqlcgen.User, org sqlcgen.Organization) *http.Request {
	ctx := context.WithValue(req.Context(), activeOrgContextKey, activeOrgContext{caller: &caller, org: org})
	return req.WithContext(ctx)
}

// withTestUser returns req with user attached to its context, as
// auth.RequireAuthAPI middleware would for an authenticated request.
func withTestUser(req *http.Request, user sqlcgen.User) *http.Request {
	return req.WithContext(auth.WithUser(req.Context(), &user))
}

// withHeadscaleClient attaches client to req's context, as
// RequireHeadscaleClient would for an authenticated request whose
// organization's headscale is reachable.
func withHeadscaleClient(req *http.Request, client *headscaleclient.Client) *http.Request {
	ctx := context.WithValue(req.Context(), headscaleClientContextKey, client)
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

func insertUserWithOrg(t *testing.T, queries *dbpkg.TxQueries) sqlcgen.User {
	t.Helper()
	user := insertUser(t, queries)
	org, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: "acme-" + xid.New().String(),
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if err := queries.SetUserOrganizationAndRole(t.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
		OrganizationID: sql.NullString{String: org.ID, Valid: true},
		Role:           RoleOwner,
		ID:             user.ID,
	}); err != nil {
		t.Fatalf("SetUserOrganizationAndRole: %v", err)
	}
	user, err = queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
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
