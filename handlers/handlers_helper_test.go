package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/hscl/hstest"
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
	return newTestHandlersWithDataDir(t, baseDomain, shortTempDir(t))
}

// shortTempDir is t.TempDir, but short enough for the unix sockets of the
// headscale instances sign-ups start, as their paths are limited to ~100
// bytes.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// newTestHandlersWithDataDir is newTestHandlersWithBaseDomain with the
// given data directory, where organizations' headscale instances keep
// their state.
func newTestHandlersWithDataDir(t *testing.T, baseDomain, dataDir string) (*Handlers, *dbpkg.TxQueries) {
	t.Helper()
	_, queries := dbtest.New(t)

	derpMap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Regions":{"900":{"RegionID":900,"RegionCode":"test","Nodes":[{"Name":"900a","RegionID":900,"HostName":"derp.example.com"}]}}}`))
	}))
	t.Cleanup(derpMap.Close)
	derpCfg := config.DerpConfig{MapURL: derpMap.URL}
	h, err := New(queries, &config.Config{
		BaseDomain:    baseDomain,
		DataDir:       dataDir,
		Derp:          derpCfg,
		ContentDomain: baseDomain,
		Funnel:        config.FunnelConfig{MaxBodyBytes: 1 << 20},
	}, nil, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = h.orgs.Close(context.Background()) })
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
	h, queries := newTestHandlersWithBaseDomain(t, testBaseDomain)
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

// realOrg is an organization whose headscale really runs in-process, as in
// production, reachable both through client and through h.orgs.
type realOrg struct {
	org    sqlcgen.Organization
	client *hscl.Client
	sock   string
}

// newRealOrgTestHandlers builds Handlers and starts organization "acme"'s
// headscale.
func newRealOrgTestHandlers(t *testing.T) (*Handlers, realOrg) {
	t.Helper()
	dataDir := shortTempDir(t)
	h, _ := newTestHandlersWithDataDir(t, testBaseDomain, dataDir)
	org, err := h.orgs.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	client, err := h.orgs.Ensure(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return h, realOrg{org: org, client: client, sock: orgs.SocketPath(dataDir, "acme")}
}

// createMember creates a member of o with the given role, provider, email
// and email verification, together with its headscale user, the way a
// sign-up through that provider does.
func (o realOrg) createMember(t *testing.T, h *Handlers, role, provider, email string, verified bool) sqlcgen.User {
	t.Helper()
	id := xid.New().String()
	params := sqlcgen.CreateUserParams{
		ID:             id,
		OrganizationID: o.org.ID,
		Role:           role,
		Provider:       provider,
		ProviderUserID: id,
		Email:          email,
		EmailVerified:  boolToInt64(verified),
	}
	user, err := h.insertUser(t.Context(), o.org.Subdomain, params, func(q *sqlcgen.Queries, params sqlcgen.CreateUserParams) (sqlcgen.User, error) {
		return q.CreateUser(t.Context(), params)
	})
	if err != nil {
		t.Fatalf("insertUser: %v", err)
	}
	return user
}

// registerMachine registers a machine named machine for member's headscale user.
func (o realOrg) registerMachine(t *testing.T, member sqlcgen.User, machine string) {
	t.Helper()
	hsUser := o.headscaleUser(t, member.HeadscaleUserID)
	if _, err := o.client.RegisterNode(t.Context(), hsUser.Name, hstest.PendingNode(t, o.sock, hsUser.Name, machine)); err != nil {
		t.Fatalf("RegisterNode(%q): %v", machine, err)
	}
}

// headscaleUser returns o's headscale user id.
func (o realOrg) headscaleUser(t *testing.T, id string) hscl.User {
	t.Helper()
	users, err := o.client.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	for _, u := range users {
		if u.ID == id {
			return u
		}
	}
	t.Fatalf("headscale user %s not found in %+v", id, users)
	return hscl.User{}
}

// headscaleUserOf returns member's headscale user ID, re-read from the database.
func headscaleUserOf(t *testing.T, h *Handlers, member sqlcgen.User) string {
	t.Helper()
	got, err := h.queries.GetUserByID(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	return got.HeadscaleUserID
}
