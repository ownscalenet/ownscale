package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/orgs"

	"github.com/rs/xid"
)

const testBaseDomain = "example.com"

// newTestHandlers builds a *Handlers backed by a real, freshly-migrated SQLite
// database. No headscale instance runs, so orgs.Service reports every
// organization as not running.
func newTestHandlers(t *testing.T) (*Handlers, *dbpkg.TxQueries) {
	t.Helper()
	return newTestHandlersWithBaseDomain(t, "")
}

// newTestHandlersWithBaseDomain is newTestHandlers, but with baseDomain
// wired into the WebAuthn RP ID/origin (e.g. testBaseDomain), matching how
// production configures it.
func newTestHandlersWithBaseDomain(t *testing.T, baseDomain string) (*Handlers, *dbpkg.TxQueries) {
	t.Helper()
	_, queries := dbtest.New(t)

	h, err := New(queries, baseDomain, t.TempDir(), nil, slog.New(slog.DiscardHandler), config.DerpConfig{}, config.AuthConfig{})
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
func withHeadscaleClient(req *http.Request, client *hscl.Client) *http.Request {
	ctx := context.WithValue(req.Context(), headscaleClientContextKey, headscaleContext{client: client})
	return req.WithContext(ctx)
}

// withHeadscaleClientOrg is withHeadscaleClient plus org, for handlers
// (e.g. funnel routes) that also read org from headscaleClientFromContext.
func withHeadscaleClientOrg(req *http.Request, client *hscl.Client, org sqlcgen.Organization) *http.Request {
	ctx := context.WithValue(req.Context(), headscaleClientContextKey, headscaleContext{client: client, org: org})
	return req.WithContext(ctx)
}

// insertOrg inserts an active organization and its headscale config, as
// orgs.Service.Create would, but without starting a headscale instance.
func insertOrg(t *testing.T, queries *dbpkg.TxQueries, subdomain string) sqlcgen.Organization {
	t.Helper()
	org := dbtest.CreateOrg(t, queries.Queries, subdomain)
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

// insertUser inserts an owner of a freshly created organization of its own.
func insertUser(t *testing.T, queries *dbpkg.TxQueries) sqlcgen.User {
	t.Helper()
	org := insertOrg(t, queries, "org"+xid.New().String())
	return dbtest.CreateUser(t, queries.Queries, org.ID, RoleOwner)
}

// newOrgTestHandlers builds Handlers, inserts organization "acme" (see
// insertOrg), and inserts one of its users per role in roles, in order.
func newOrgTestHandlers(t *testing.T, roles ...string) (*Handlers, sqlcgen.Organization, []sqlcgen.User) {
	t.Helper()
	h, queries := newTestHandlers(t)
	org := insertOrg(t, queries, "acme")
	users := make([]sqlcgen.User, 0, len(roles))
	for _, role := range roles {
		users = append(users, dbtest.CreateUser(t, queries.Queries, org.ID, role))
	}
	return h, org, users
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
