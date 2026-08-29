package auth

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/pressly/goose/v3"
	"github.com/rs/xid"

	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// newTestQueries opens a temp-file SQLite database and applies the real
// embedded migrations via goose, mirroring cmd/osa's newTestDB helper, so
// these tests exercise the middleware against a real *sqlcgen.Queries
// backed by real SQL rather than a hand-rolled fake.
func newTestQueries(t *testing.T) *sqlcgen.Queries {
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return dbpkg.New(pool, logger).Queries
}

// insertUser inserts a user row directly and returns it.
func insertUser(t *testing.T, queries *sqlcgen.Queries) sqlcgen.User {
	t.Helper()
	id := xid.New().String()
	user, err := queries.CreateUser(t.Context(), sqlcgen.CreateUserParams{
		ID:             id,
		Provider:       "oidc",
		ProviderUserID: id,
		Email:          id + "@example.com",
		Name:           "Test User",
		AvatarUrl:      "",
	})
	if err != nil {
		t.Fatalf("insertUser: %v", err)
	}
	return user
}

// setupSessions calls Setup once for the test binary so gothic.Store is
// initialized; safe to call multiple times.
func setupSessions() {
	Setup("test-session-secret-at-least-32-bytes-long")
}

func TestWithUserContext(t *testing.T) {
	_, ok := UserFromContext(t.Context())
	if ok {
		t.Fatal("UserFromContext on bare context: want ok=false")
	}

	user := &sqlcgen.User{ID: "u1"}
	ctx := WithUser(t.Context(), user)
	got, ok := UserFromContext(ctx)
	if !ok {
		t.Fatal("UserFromContext after WithUser: want ok=true")
	}
	if got != user {
		t.Errorf("UserFromContext = %+v, want the same *User set via WithUser", got)
	}
}

func TestRequireAuthAPINoSession(t *testing.T) {
	setupSessions()
	queries := newTestQueries(t)

	called := false
	h := RequireAuthAPI(queries, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	h(rec, req)

	if called {
		t.Fatal("next handler should not be called without a session")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
}

func TestRequireAuthAPIValidSession(t *testing.T) {
	setupSessions()
	queries := newTestQueries(t)
	user := insertUser(t, queries)

	// Mint a session cookie via StoreUserSession, then present it on a
	// subsequent request, mirroring how a real browser round-trip works.
	storeRec := httptest.NewRecorder()
	storeReq := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := StoreUserSession(storeRec, storeReq, user.ID); err != nil {
		t.Fatalf("StoreUserSession: %v", err)
	}

	var gotUser *sqlcgen.User
	h := RequireAuthAPI(queries, func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			t.Error("expected a user in context")
		}
		gotUser = u
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	for _, c := range storeRec.Result().Cookies() {
		req.AddCookie(c)
	}
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotUser == nil || gotUser.ID != user.ID {
		t.Errorf("got user %+v, want id %q", gotUser, user.ID)
	}
}

func TestRequireAuthAPISessionForDeletedUser(t *testing.T) {
	setupSessions()
	queries := newTestQueries(t)

	storeRec := httptest.NewRecorder()
	storeReq := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := StoreUserSession(storeRec, storeReq, "does-not-exist"); err != nil {
		t.Fatalf("StoreUserSession: %v", err)
	}

	called := false
	h := RequireAuthAPI(queries, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	for _, c := range storeRec.Result().Cookies() {
		req.AddCookie(c)
	}
	h(rec, req)

	if called {
		t.Fatal("next handler should not be called for a session referencing a nonexistent user")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (not 500)", rec.Code, http.StatusUnauthorized)
	}
}

func TestClearUserSessionInvalidatesSession(t *testing.T) {
	setupSessions()
	queries := newTestQueries(t)
	user := insertUser(t, queries)

	storeRec := httptest.NewRecorder()
	storeReq := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := StoreUserSession(storeRec, storeReq, user.ID); err != nil {
		t.Fatalf("StoreUserSession: %v", err)
	}
	cookies := storeRec.Result().Cookies()

	// Present the stored cookie to a request, then clear the session on
	// that same request/response pair (mirroring Logout: it reads the
	// incoming cookie via gothic.Store and writes a cleared one back).
	clearRec := httptest.NewRecorder()
	clearReq := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	for _, c := range cookies {
		clearReq.AddCookie(c)
	}
	if err := ClearUserSession(clearRec, clearReq); err != nil {
		t.Fatalf("ClearUserSession: %v", err)
	}

	called := false
	h := RequireAuthAPI(queries, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	for _, c := range clearRec.Result().Cookies() {
		req.AddCookie(c)
	}
	h(rec, req)

	if called {
		t.Fatal("next handler should not be called after ClearUserSession")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestSetupCookieOptions(t *testing.T) {
	setupSessions()

	store, ok := gothic.Store.(*sessions.CookieStore)
	if !ok {
		t.Fatalf("gothic.Store = %T, want *sessions.CookieStore", gothic.Store)
	}
	if !store.Options.HttpOnly {
		t.Error("Options.HttpOnly = false, want true")
	}
	if !store.Options.Secure {
		t.Error("Options.Secure = false, want true")
	}
	if store.Options.SameSite != http.SameSiteLaxMode {
		t.Errorf("Options.SameSite = %v, want http.SameSiteLaxMode", store.Options.SameSite)
	}
}

// newTestOIDCServer spins up a plain HTTP test server serving a minimal
// OIDC discovery document, so RegisterUpstreamOIDC can be exercised
// end-to-end against a real HTTP GET. Plain HTTP (rather than TLS) is fine
// here: trust-store behavior is no longer RegisterUpstreamOIDC's concern
// (see its doc comment) -- that's established at the OS level, ahead of
// time, via LocalCADir/SSL_CERT_DIR (see localca and
// docker-compose.yml).
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

func TestRegisterUpstreamOIDC(t *testing.T) {
	srv := newTestOIDCServer(t)

	if err := RegisterUpstreamOIDC(srv.URL, "client-id", "client-secret", "https://example.com/auth/oidc/callback"); err != nil {
		t.Fatalf("RegisterUpstreamOIDC: %v", err)
	}
	if _, err := goth.GetProvider(UpstreamProviderName); err != nil {
		t.Errorf("GetProvider(%q) after RegisterUpstreamOIDC: %v", UpstreamProviderName, err)
	}
}

func TestRegisterGoogle(t *testing.T) {
	if err := RegisterGoogle("client-id", "client-secret", "https://example.com/auth/google/callback"); err != nil {
		t.Fatalf("RegisterGoogle: %v", err)
	}
	if _, err := goth.GetProvider(GoogleProviderName); err != nil {
		t.Errorf("GetProvider(%q) after RegisterGoogle: %v", GoogleProviderName, err)
	}
}

func TestRegisterMicrosoft(t *testing.T) {
	if err := RegisterMicrosoft("client-id", "client-secret", "https://example.com/auth/microsoft/callback"); err != nil {
		t.Fatalf("RegisterMicrosoft: %v", err)
	}
	if _, err := goth.GetProvider(MicrosoftProviderName); err != nil {
		t.Errorf("GetProvider(%q) after RegisterMicrosoft: %v", MicrosoftProviderName, err)
	}
}
