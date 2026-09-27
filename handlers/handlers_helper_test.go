package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/headscaleclient"
	"github.com/ownscalenet/ownscale/orgs"

	"github.com/rs/xid"
)

// port is required and unique so use atomic to prevent race conditions
// in concurrent tests.
var nextTestPort atomic.Int64

func testPort() int64 { return 20_000 + nextTestPort.Add(1) - 1 }

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

const testBaseDomain = "example.com"

// newTestHandlers builds a *Handlers backed by a real, freshly-migrated SQLite
// database, so organization provisioning always works and every user is
// guaranteed a real, active organization.
func newTestHandlers(t *testing.T) (*Handlers, *dbpkg.TxQueries) {
	t.Helper()
	return newTestHandlersWithBaseDomain(t, "")
}

// newTestHandlersWithBaseDomain is newTestHandlers, but with baseDomain
// wired into the WebAuthn RP ID/origin (e.g. testBaseDomain), matching how
// production configures it.
func newTestHandlersWithBaseDomain(t *testing.T, baseDomain string) (*Handlers, *dbpkg.TxQueries) {
	t.Helper()
	queries := newTestQueries(t)

	h, err := New(queries, baseDomain, t.TempDir(), nil, discardLogger(), config.DerpConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h, queries
}

// withTestUser returns req with user attached to its context, as
// auth.RequireAuthAPI middleware would for an authenticated request.
func withTestUser(req *http.Request, user sqlcgen.User) *http.Request {
	return req.WithContext(auth.WithUser(req.Context(), &user))
}

// withHeadscaleClient attaches client to req's context, as
// RequireHeadscaleClient would for an authenticated request whose
// organization's headscale is reachable. Handlers that only read the
// client (not org) from context are fine with org's zero value.
func withHeadscaleClient(req *http.Request, client *headscaleclient.Client) *http.Request {
	ctx := context.WithValue(req.Context(), headscaleClientContextKey, headscaleContext{client: client})
	return req.WithContext(ctx)
}

// withHeadscaleClientOrg is withHeadscaleClient plus org, for handlers
// (e.g. funnel routes) that also read org from headscaleClientFromContext.
func withHeadscaleClientOrg(req *http.Request, client *headscaleclient.Client, org sqlcgen.Organization) *http.Request {
	ctx := context.WithValue(req.Context(), headscaleClientContextKey, headscaleContext{client: client, org: org})
	return req.WithContext(ctx)
}

// insertOrg inserts an active organization row and its headscale config
// directly, as orgs.Service.Create would, but without starting a headscale
// instance for it.
func insertOrg(t *testing.T, queries *dbpkg.TxQueries, subdomain string) sqlcgen.Organization {
	t.Helper()
	org, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: subdomain,
		Status:    orgs.StatusActive,
		Port:      testPort(),
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	hsCfg := &orgs.HeadscaleConfig{
		DNS: orgs.DNSConfig{
			MagicDNS:    true,
			BaseDomain:  subdomain + ".ownscale",
			Nameservers: orgs.Nameservers{Global: []string{"1.1.1.1"}},
		},
		OIDC: orgs.OIDCConfig{Issuer: "https://auth.example.com"},
	}
	hsConfig, err := hsCfg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := queries.CreateOrgConfig(t.Context(), sqlcgen.CreateOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: org.ID,
		Path:           orgs.HeadscaleConfigKey,
		Content:        hsConfig,
	}); err != nil {
		t.Fatalf("CreateOrgConfig: %v", err)
	}
	return org
}

// insertUser inserts a user row, together with a freshly created organization
// it owns (every user always belongs to one), and returns the user.
func insertUser(t *testing.T, queries *dbpkg.TxQueries) sqlcgen.User {
	t.Helper()
	org := insertOrg(t, queries, "acme-"+xid.New().String())
	id := xid.New().String()
	user, err := queries.CreateUser(t.Context(), sqlcgen.CreateUserParams{
		ID:             id,
		OrganizationID: org.ID,
		Role:           RoleOwner,
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
