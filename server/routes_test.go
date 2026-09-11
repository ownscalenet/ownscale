package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/handlers"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestQueries returns queries backed by a copy of the shared, already
// migrated dbtest template (see db/dbtest), avoiding a full goose run per
// test.
func newTestQueries(t *testing.T) *dbpkg.TxQueries {
	t.Helper()

	pool, queries := dbtest.New(t)
	// Migration 000008 seeds placeholder organizations (aaaa..ffff) so a real
	// deployment's provisioner has something to recover at startup; tests
	// assume a clean organizations table, so remove them here.
	if _, err := pool.ExecContext(t.Context(), `DELETE FROM organizations WHERE id LIKE 'seed-%'`); err != nil {
		t.Fatalf("clear seeded organizations: %v", err)
	}

	return queries
}

func newTestMux(t *testing.T) http.Handler {
	t.Helper()
	queries := newTestQueries(t)
	h, err := handlers.New(queries, nil, "", "", nil, discardLogger(), config.DerpConfig{}, config.ProvisionerConfig{})
	if err != nil {
		t.Fatalf("handlers.New: %v", err)
	}
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

func TestNewMuxUnmatchedPathFallsBackToStatic(t *testing.T) {
	queries := newTestQueries(t)
	h, err := handlers.New(queries, nil, "", "", nil, discardLogger(), config.DerpConfig{}, config.ProvisionerConfig{})
	if err != nil {
		t.Fatalf("handlers.New: %v", err)
	}
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
	h, err := handlers.New(queries, nil, "", "", nil, discardLogger(), config.DerpConfig{}, config.ProvisionerConfig{})
	if err != nil {
		t.Fatalf("handlers.New: %v", err)
	}
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	mux := NewMux(h, requireAuthAPI, discardLogger(), http.NotFoundHandler(), nil)

	auth.Setup("test-session-secret-at-least-32-bytes-long")
	id := xid.New().String()
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
