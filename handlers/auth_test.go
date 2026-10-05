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
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/orgs"

	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
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
	// Its headscale starts on first use, not at sign-up.
	if got := h.orgs.Status(org.Subdomain); got != orgs.StatusUnprovisioned {
		t.Errorf("organization status = %q, want %q", got, orgs.StatusUnprovisioned)
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
	owner := insertUser(t, queries)
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
	h, err := New(queries, "", t.TempDir(), nil, slog.New(slog.DiscardHandler), config.DerpConfig{}, config.AuthConfig{
		UpstreamOIDC: config.UpstreamOIDCConfig{Issuer: "https://idp.example.com", ClientID: "oidc-id"},
		Google:       config.GoogleConfig{ClientID: "google-id"},
		// Microsoft left unset: Enabled() should report false.
	}, config.FunnelConfig{})
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
	want := map[string]bool{"oidc": true, "google": true, "microsoft": false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AuthProviders response = %+v, want %+v", got, want)
	}
}
