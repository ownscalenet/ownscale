package server

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/google/uuid"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/handlers"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestQueries opens a temp-file SQLite database and applies the real
// embedded migrations via goose.
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

func newTestMux(t *testing.T) http.Handler {
	t.Helper()
	queries := newTestQueries(t)
	h := handlers.New(queries, nil, "", nil, discardLogger(), config.DerpConfig{})
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	return NewMux(h, requireAuthAPI, discardLogger(), http.NotFoundHandler(), nil)
}

func TestNewMuxProtectedRoutesRequireAuth(t *testing.T) {
	mux := newTestMux(t)

	protected := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/session"},
		{http.MethodPost, "/api/logout"},
		{http.MethodPost, "/api/organizations"},
	}
	for _, p := range protected {
		req := httptest.NewRequest(p.method, p.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want %d", p.method, p.path, rec.Code, http.StatusUnauthorized)
		}
	}
}

func TestNewMuxTLSAskUnauthenticated(t *testing.T) {
	mux := newTestMux(t)

	req := httptest.NewRequest(http.MethodGet, "/api/tls-ask?domain=x", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// h.baseDomain is "" here, so TLSAsk itself 403s; the point of this
	// test is just that it's reachable at all without a session (not 401).
	if rec.Code == http.StatusUnauthorized {
		t.Errorf("status = %d, want reachable unauthenticated (not 401)", rec.Code)
	}
}

func TestNewMuxUnmatchedPathFallsBackToStatic(t *testing.T) {
	queries := newTestQueries(t)
	h := handlers.New(queries, nil, "", nil, discardLogger(), config.DerpConfig{})
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}

	staticCalled := false
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staticCalled = true
		w.WriteHeader(http.StatusTeapot)
	})
	mux := NewMux(h, requireAuthAPI, discardLogger(), static, nil)

	req := httptest.NewRequest(http.MethodGet, "/some/spa/route", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if !staticCalled {
		t.Fatal("expected the unmatched path to fall through to the static handler")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
}

func TestNewMuxAuthenticatedSession(t *testing.T) {
	queries := newTestQueries(t)
	h := handlers.New(queries, nil, "", nil, discardLogger(), config.DerpConfig{})
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	mux := NewMux(h, requireAuthAPI, discardLogger(), http.NotFoundHandler(), nil)

	auth.Setup("test-session-secret-at-least-32-bytes-long")
	id := uuid.Must(uuid.NewV7()).String()
	user, err := queries.CreateUser(t.Context(), sqlcgen.CreateUserParams{
		ID:             id,
		Provider:       "oidc",
		ProviderUserID: id,
		Email:          id + "@example.com",
		Name:           "Test User",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	storeRec := httptest.NewRecorder()
	if err := auth.StoreUserSession(storeRec, httptest.NewRequest(http.MethodGet, "/", nil), user.ID); err != nil {
		t.Fatalf("StoreUserSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	for _, c := range storeRec.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}
