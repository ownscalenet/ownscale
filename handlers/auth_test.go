package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/auth/oidcprovider"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/orgs"

	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/rs/xid"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// newTestOIDCServer spins up a minimal OIDC discovery server so
// auth.RegisterUpstreamOIDC (and therefore gothic.BeginAuthHandler) has a
// real "oidc" provider to work with in BeginAuth tests.
func newTestOIDCServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issuer": "` + srv.URL + `",
			"authorization_endpoint": "` + srv.URL + `/auth",
			"token_endpoint": "` + srv.URL + `/token",
			"userinfo_endpoint": "` + srv.URL + `/userinfo"
		}`))
	})
	return srv
}

// registerTestOIDCProvider registers a real "oidc" goth provider (backed by
// a fake discovery server) so gothic.BeginAuthHandler has something to
// redirect to.
func registerTestOIDCProvider(t *testing.T) {
	t.Helper()
	srv := newTestOIDCServer(t)
	if err := auth.RegisterUpstreamOIDC(srv.URL, "client-id", "client-secret", "https://example.com/auth/oidc/callback"); err != nil {
		t.Fatalf("RegisterUpstreamOIDC: %v", err)
	}
}

// TestBeginAuthSetsNextCookie verifies that BeginAuth stores the "next"
// redirect target in a dedicated cookie (rather than gothic's own session,
// which gets clobbered -- see nextCookieName's doc comment) and that the
// cookie survives even after gothic.BeginAuthHandler does its own
// session read/write.
func TestBeginAuthSetsNextCookie(t *testing.T) {
	setupSessions()
	registerTestOIDCProvider(t)
	h, _ := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc?next="+"%2Finvite%2Ftesttoken", nil)
	req.SetPathValue("provider", "oidc")
	rec := httptest.NewRecorder()

	h.BeginAuth(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
	}

	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == nextCookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatalf("expected a %q cookie to be set, cookies: %+v", nextCookieName, rec.Result().Cookies())
	}
	if found.Value != "/invite/testtoken" {
		t.Errorf("%s cookie value = %q, want %q", nextCookieName, found.Value, "/invite/testtoken")
	}
	if !found.HttpOnly || !found.Secure {
		t.Errorf("%s cookie should be HttpOnly and Secure, got %+v", nextCookieName, found)
	}
}

// TestBeginAuthNoNextParam verifies no next cookie is set when the query
// string doesn't include one, so we don't leak a stale empty cookie.
func TestBeginAuthNoNextParam(t *testing.T) {
	setupSessions()
	registerTestOIDCProvider(t)
	h, _ := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc", nil)
	req.SetPathValue("provider", "oidc")
	rec := httptest.NewRecorder()

	h.BeginAuth(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == nextCookieName {
			t.Errorf("did not expect a %q cookie without a next param, got %+v", nextCookieName, c)
		}
	}
}

func TestAuthCallbackRedirectsToNextCookie(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)
	user := insertUser(t, queries)

	orig := gothic.CompleteUserAuth
	t.Cleanup(func() { gothic.CompleteUserAuth = orig })
	gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
		return goth.User{
			Provider:  "oidc",
			UserID:    user.ProviderUserID,
			Email:     user.Email,
			Name:      user.Name,
			AvatarURL: user.AvatarUrl,
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", nil)
	req.AddCookie(&http.Cookie{Name: nextCookieName, Value: "/invite/testtoken"})
	rec := httptest.NewRecorder()

	h.AuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/invite/testtoken" {
		t.Errorf("Location = %q, want %q (this is the bug: it must not fall back to /)", loc, "/invite/testtoken")
	}

	// The cookie must be cleared (single-use) once consumed.
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == nextCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("expected %q cookie to be cleared after AuthCallback consumes it", nextCookieName)
	}
}

// TestAuthCallbackNoNextCookieFallsBackToRoot verifies the ordinary case
// (no next cookie present, e.g. a plain login with no redirect target)
// still falls back to "/".
func TestAuthCallbackNoNextCookieFallsBackToRoot(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)
	user := insertUser(t, queries)

	orig := gothic.CompleteUserAuth
	t.Cleanup(func() { gothic.CompleteUserAuth = orig })
	gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
		return goth.User{
			Provider:  "oidc",
			UserID:    user.ProviderUserID,
			Email:     user.Email,
			Name:      user.Name,
			AvatarURL: user.AvatarUrl,
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", nil)
	rec := httptest.NewRecorder()

	h.AuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}
}

// TestAuthCallbackDisplayName verifies the account's name falls back to the
// provider login when the provider reports no display name, as GitHub does
// for accounts without one.
func TestAuthCallbackDisplayName(t *testing.T) {
	tests := []struct {
		name, gothName, nickName, want string
	}{
		{"display name", "Mona Lisa", "octocat", "Mona Lisa"},
		{"login only", "", "octocat", "octocat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupSessions()
			h, queries := newTestHandlers(t)
			user := insertUser(t, queries)

			orig := gothic.CompleteUserAuth
			t.Cleanup(func() { gothic.CompleteUserAuth = orig })
			gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
				return goth.User{
					Provider: user.Provider,
					UserID:   user.ProviderUserID,
					Email:    user.Email,
					Name:     tt.gothName,
					NickName: tt.nickName,
				}, nil
			}

			rec := httptest.NewRecorder()
			h.AuthCallback(rec, httptest.NewRequest(http.MethodGet, "/auth/github/callback", nil))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body)
			}
			got, err := queries.GetUserByID(t.Context(), user.ID)
			if err != nil {
				t.Fatalf("GetUserByID: %v", err)
			}
			if got.Name != tt.want {
				t.Errorf("Name = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

var randomSubdomainRe = regexp.MustCompile(`^[a-z0-9]+$`)

func TestAuthCallbackAutoAssignsOrganization(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)

	orig := gothic.CompleteUserAuth
	t.Cleanup(func() { gothic.CompleteUserAuth = orig })
	gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
		return goth.User{
			Provider: "oidc",
			UserID:   "new-user-id",
			Email:    "new-user@example.com",
			Name:     "New User",
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", nil)
	rec := httptest.NewRecorder()

	h.AuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	user, err := queries.GetUserByProvider(t.Context(), sqlcgen.GetUserByProviderParams{
		Provider:       "oidc",
		ProviderUserID: "new-user-id",
	})
	if err != nil {
		t.Fatalf("GetUserByProvider: %v", err)
	}
	if user.OrganizationID == "" {
		t.Fatal("OrganizationID: want it set after AuthCallback, got unset")
	}
	if user.Role != RoleOwner {
		t.Errorf("Role = %q, want %q", user.Role, RoleOwner)
	}

	org, err := queries.GetOrganization(t.Context(), user.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	// Its headscale starts at sign-up, to create the owner's headscale user.
	if got := h.orgs.Status(org.Subdomain); got != orgs.StatusActive {
		t.Errorf("organization status = %q, want %q", got, orgs.StatusActive)
	}
	if !randomSubdomainRe.MatchString(org.Subdomain) {
		t.Errorf("organization subdomain = %q, want match for %s", org.Subdomain, randomSubdomainRe)
	}
}

// TestAuthCallbackAssignsInvitedOrganization verifies that a first-time
// login redirected via /login?next=/invite/{token} (see AcceptInvite.tsx)
// is created with the invite's organization and role directly.
func TestAuthCallbackAssignsInvitedOrganization(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)
	owner := createOwner(t, h)
	org, err := queries.GetOrganization(t.Context(), owner.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	inv, err := queries.CreateInvite(t.Context(), sqlcgen.CreateInviteParams{
		ID:             "invite-1",
		OrganizationID: org.ID,
		Role:           "admin",
		Token:          "testtoken",
		CreatedBy:      owner.ID,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	orig := gothic.CompleteUserAuth
	t.Cleanup(func() { gothic.CompleteUserAuth = orig })
	gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
		return goth.User{
			Provider: "oidc",
			UserID:   "invitee-user-id",
			Email:    "invitee@example.com",
			Name:     "Invitee User",
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", nil)
	req.AddCookie(&http.Cookie{Name: nextCookieName, Value: "/invite/testtoken"})
	rec := httptest.NewRecorder()

	h.AuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/invite/testtoken" {
		t.Errorf("Location = %q, want %q", loc, "/invite/testtoken")
	}

	user, err := queries.GetUserByProvider(t.Context(), sqlcgen.GetUserByProviderParams{
		Provider:       "oidc",
		ProviderUserID: "invitee-user-id",
	})
	if err != nil {
		t.Fatalf("GetUserByProvider: %v", err)
	}
	if user.OrganizationID != org.ID {
		t.Errorf("OrganizationID = %q, want the invited organization %q", user.OrganizationID, org.ID)
	}
	if user.Role != "admin" {
		t.Errorf("Role = %q, want the invite's role %q", user.Role, "admin")
	}

	acceptedInv, err := queries.GetInvite(t.Context(), inv.ID)
	if err != nil {
		t.Fatalf("GetInvite: %v", err)
	}
	if !acceptedInv.AcceptedAt.Valid {
		t.Error("invite AcceptedAt not set, want it marked accepted")
	}
	if acceptedInv.AcceptedByUserID.String != user.ID {
		t.Errorf("invite AcceptedByUserID = %+v, want %q", acceptedInv.AcceptedByUserID, user.ID)
	}
}

// TestAuthCallbackMaxOrgs verifies that once OWNSCALE_MAX_ORGS
// organizations exist, a first-time login without an invite is refused,
// while an invited one still joins.
func TestAuthCallbackMaxOrgs(t *testing.T) {
	tests := []struct {
		name       string
		next       string
		wantStatus int
	}{
		{name: "without invite", wantStatus: http.StatusForbidden},
		{name: "with invite", next: "/invite/testtoken", wantStatus: http.StatusSeeOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupSessions()
			h, queries := newTestHandlersWithConfig(t, "", shortTempDir(t), func(c *config.Config) { c.MaxOrgs = 1 })
			owner := createOwner(t, h)
			if _, err := queries.CreateInvite(t.Context(), sqlcgen.CreateInviteParams{
				ID:             "invite-1",
				OrganizationID: owner.OrganizationID,
				Role:           RoleMember,
				Token:          "testtoken",
				CreatedBy:      owner.ID,
				ExpiresAt:      time.Now().Add(24 * time.Hour),
			}); err != nil {
				t.Fatalf("CreateInvite: %v", err)
			}

			orig := gothic.CompleteUserAuth
			t.Cleanup(func() { gothic.CompleteUserAuth = orig })
			gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
				return goth.User{Provider: "oidc", UserID: "second-user-id", Email: "second@example.com", Name: "Second"}, nil
			}

			req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", nil)
			if tt.next != "" {
				req.AddCookie(&http.Cookie{Name: nextCookieName, Value: tt.next})
			}
			rec := httptest.NewRecorder()
			h.AuthCallback(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			user, err := queries.GetUserByProvider(t.Context(), sqlcgen.GetUserByProviderParams{
				Provider:       "oidc",
				ProviderUserID: "second-user-id",
			})
			switch {
			case tt.wantStatus == http.StatusForbidden && !errors.Is(err, sql.ErrNoRows):
				t.Errorf("GetUserByProvider: err = %v, want sql.ErrNoRows for a refused sign-up", err)
			case tt.wantStatus != http.StatusForbidden && err != nil:
				t.Fatalf("GetUserByProvider: %v", err)
			case tt.wantStatus != http.StatusForbidden && user.OrganizationID != owner.OrganizationID:
				t.Errorf("OrganizationID = %q, want the invited organization %q", user.OrganizationID, owner.OrganizationID)
			}
			n, err := queries.CountOrganizations(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Errorf("organizations = %d, want 1", n)
			}
		})
	}
}

func TestAuthCallbackRejectsInvalidInvite(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)

	orig := gothic.CompleteUserAuth
	t.Cleanup(func() { gothic.CompleteUserAuth = orig })
	gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
		return goth.User{
			Provider: "oidc",
			UserID:   "invitee-user-id",
			Email:    "invitee@example.com",
			Name:     "Invitee User",
		}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", nil)
	req.AddCookie(&http.Cookie{Name: nextCookieName, Value: "/invite/no-such-token"})
	rec := httptest.NewRecorder()

	h.AuthCallback(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusGone, rec.Body.String())
	}
	if _, err := queries.GetUserByProvider(t.Context(), sqlcgen.GetUserByProviderParams{
		Provider:       "oidc",
		ProviderUserID: "invitee-user-id",
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetUserByProvider: err = %v, want sql.ErrNoRows (no account should be created)", err)
	}
}

// newTestOIDCStorage builds a real *oidcprovider.Storage (backed by a fresh
// signing key in t.TempDir()) wired to queries, so finishLogin's OIDC
// authorize-request wiring can be exercised end-to-end.
func newTestOIDCStorage(t *testing.T, queries *sqlcgen.Queries) *oidcprovider.Storage {
	t.Helper()
	cfg := &config.Config{
		Auth: config.AuthConfig{
			SessionSecret: "test-session-secret-at-least-32-bytes-long",
			OIDCProvider: config.OIDCProviderConfig{
				Issuer:              "https://auth.example.com",
				SigningKeyPath:      t.TempDir() + "/oidc_signing_key.pem",
				AccessTokenLifetime: "5m",
				IDTokenLifetime:     "1h",
			},
		},
	}
	_, storage, err := oidcprovider.New(cfg, queries)
	if err != nil {
		t.Fatalf("oidcprovider.New: %v", err)
	}
	return storage
}

// insertTestOIDCClient inserts an OIDC client (as provisioned for a
// headscale/tailnet login-server) belonging to orgID.
func insertTestOIDCClient(t *testing.T, queries *sqlcgen.Queries, orgID string) sqlcgen.OidcClient {
	t.Helper()
	client, err := queries.CreateOIDCClient(t.Context(), sqlcgen.CreateOIDCClientParams{
		ID:               xid.New().String(),
		OrganizationID:   orgID,
		ClientID:         "client-" + xid.New().String(),
		ClientSecretHash: "unused",
		RedirectUris:     `["https://acme.example.com/oidc/callback"]`,
	})
	if err != nil {
		t.Fatalf("CreateOIDCClient: %v", err)
	}
	return client
}

// TestFinishLoginRejectsNonMemberOIDCAuthRequest verifies that finishing
// login with a "next" pointing at a pending OIDC authorize request (as
// created by `tailscale up --login-server=...`) for a client belonging to
// a different organization than the signed-in user is rejected with 403.
func TestFinishLoginRejectsNonMemberOIDCAuthRequest(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)
	storage := newTestOIDCStorage(t, queries.Queries)
	h.oidc = storage

	acme := insertUser(t, queries)
	otherOrgUser := insertUser(t, queries)
	client := insertTestOIDCClient(t, queries.Queries, acme.OrganizationID)

	authReq, err := storage.CreateAuthRequest(t.Context(), &oidc.AuthRequest{
		ClientID:     client.ClientID,
		RedirectURI:  "https://acme.example.com/oidc/callback",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       []string{oidc.ScopeOpenID},
	}, "")
	if err != nil {
		t.Fatalf("CreateAuthRequest: %v", err)
	}
	next := "/authorize/callback?id=" + authReq.GetID()

	req := httptest.NewRequest(http.MethodGet, "/auth/callback", nil)
	rec := httptest.NewRecorder()
	h.finishLogin(rec, req, otherOrgUser, next)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
}

// TestFinishLoginAuthorizesMemberOIDCAuthRequest is
// TestFinishLoginRejectsNonMemberOIDCAuthRequest's counterpart: a member of
// the client's organization successfully authorizes the pending request and
// is redirected to its callback.
func TestFinishLoginAuthorizesMemberOIDCAuthRequest(t *testing.T) {
	setupSessions()
	h, queries := newTestHandlers(t)
	storage := newTestOIDCStorage(t, queries.Queries)
	h.oidc = storage

	member := insertUser(t, queries)
	client := insertTestOIDCClient(t, queries.Queries, member.OrganizationID)

	authReq, err := storage.CreateAuthRequest(t.Context(), &oidc.AuthRequest{
		ClientID:     client.ClientID,
		RedirectURI:  "https://acme.example.com/oidc/callback",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       []string{oidc.ScopeOpenID},
	}, "")
	if err != nil {
		t.Fatalf("CreateAuthRequest: %v", err)
	}
	next := "/authorize/callback?id=" + authReq.GetID()

	req := httptest.NewRequest(http.MethodGet, "/auth/callback", nil)
	rec := httptest.NewRecorder()
	h.finishLogin(rec, req, member, next)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != next {
		t.Errorf("Location = %q, want %q", loc, next)
	}
}

func TestLogout(t *testing.T) {
	h, queries := newTestHandlers(t)
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	req = authenticatedRequest(t, req, user.ID)

	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want %q", loc, "/login")
	}
}

func TestAPILogout(t *testing.T) {
	h, queries := newTestHandlers(t)
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req = authenticatedRequest(t, req, user.ID)

	rec := httptest.NewRecorder()
	h.APILogout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

// TestAuthProviders verifies the login page's capabilities endpoint
// reports each provider's configured state, so the frontend knows which
// buttons to grey out.
func TestAuthProviders(t *testing.T) {
	_, queries := dbtest.New(t)
	h, err := New(queries, &config.Config{
		DataDir: t.TempDir(),
		Auth: config.AuthConfig{
			UpstreamOIDC: config.UpstreamOIDCConfig{Issuer: "https://idp.example.com", ClientID: "oidc-id"},
			Google:       config.GoogleConfig{ClientID: "google-id"},
			GitHub:       config.GitHubConfig{ClientID: "github-id"},
			// Microsoft left unset: Enabled() should report false.
		},
	}, nil, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	rec := httptest.NewRecorder()
	h.AuthProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := map[string]bool{"oidc": true, "google": true, "microsoft": false, "github": true, "passkey": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AuthProviders response = %+v, want %+v", got, want)
	}
}

// TestAuthProvidersPasskeyDisabled verifies OWNSCALE_AUTH_PASSKEY_DISABLED
// both flips the providers endpoint's "passkey" flag and makes every
// passkey endpoint reject requests, so a deployment can fully turn it off.
func TestAuthProvidersPasskeyDisabled(t *testing.T) {
	_, queries := dbtest.New(t)
	h, err := New(queries, &config.Config{
		DataDir: t.TempDir(),
		Auth:    config.AuthConfig{Passkey: config.PasskeyConfig{Disabled: true}},
	}, nil, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	rec := httptest.NewRecorder()
	h.AuthProviders(rec, req)
	var got map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["passkey"] {
		t.Errorf("AuthProviders[passkey] = true, want false when disabled")
	}

	for name, fn := range map[string]http.HandlerFunc{
		"PasskeyRegisterBegin":  h.PasskeyRegisterBegin,
		"PasskeyRegisterFinish": h.PasskeyRegisterFinish,
		"PasskeyLoginBegin":     h.PasskeyLoginBegin,
		"PasskeyLoginFinish":    h.PasskeyLoginFinish,
		"PasskeyAddBegin":       h.PasskeyAddBegin,
		"PasskeyAddFinish":      h.PasskeyAddFinish,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/passkey/x", strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		fn(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s status = %d, want %d", name, rec.Code, http.StatusForbidden)
		}
	}
}

func TestIsSafeNext(t *testing.T) {
	tests := []struct {
		next string
		want bool
	}{
		{"/", true},
		{"/machines?filter=online", true},
		{"/invite/testtoken", true},
		{"/authorize/callback?id=1", true},
		{"", false},
		{"machines", false},
		{"//evil.com", false},
		{`/\evil.com`, false},
		{`\\evil.com`, false},
		{`/a\b`, false},
		{"https://evil.com", false},
		{"/a\nb", false},
		{"/a\tb", false},
	}
	for _, tt := range tests {
		if got := isSafeNext(tt.next); got != tt.want {
			t.Errorf("isSafeNext(%q) = %v, want %v", tt.next, got, tt.want)
		}
	}
}

func TestSubdomainHint(t *testing.T) {
	tests := []struct {
		name string
		user sqlcgen.CreateUserParams
		want string
	}{
		{"verified OIDC email", sqlcgen.CreateUserParams{Provider: "oidc", Email: "aaaa@ownscale.test", EmailVerified: 1}, "aaaa"},
		{"unverified email", sqlcgen.CreateUserParams{Provider: "oidc", Email: "aaaa@ownscale.test"}, ""},
		{"passkey username", sqlcgen.CreateUserParams{Provider: passkeyProvider, Email: "aaaa@passkey", EmailVerified: 1}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subdomainHint(tt.user); got != tt.want {
				t.Errorf("subdomainHint = %q, want %q", got, tt.want)
			}
		})
	}
}
