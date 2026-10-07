package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/handlers"
)

func newTestMux(t *testing.T) http.Handler {
	t.Helper()
	_, queries := dbtest.New(t)
	h, err := handlers.New(queries, "", "", nil, slog.New(slog.DiscardHandler), config.DerpConfig{}, config.AuthConfig{}, config.FunnelConfig{}, nil)
	if err != nil {
		t.Fatalf("handlers.New: %v", err)
	}
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	return NewMux(h, requireAuthAPI, slog.New(slog.DiscardHandler), http.NotFoundHandler(), nil)
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
	_, queries := dbtest.New(t)
	h, err := handlers.New(queries, "", "", nil, slog.New(slog.DiscardHandler), config.DerpConfig{}, config.AuthConfig{}, config.FunnelConfig{}, nil)
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
	mux := NewMux(h, requireAuthAPI, slog.New(slog.DiscardHandler), static, nil)

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
	_, queries := dbtest.New(t)
	h, err := handlers.New(queries, "", "", nil, slog.New(slog.DiscardHandler), config.DerpConfig{}, config.AuthConfig{}, config.FunnelConfig{}, nil)
	if err != nil {
		t.Fatalf("handlers.New: %v", err)
	}
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	mux := NewMux(h, requireAuthAPI, slog.New(slog.DiscardHandler), http.NotFoundHandler(), nil)

	auth.Setup("test-session-secret-at-least-32-bytes-long")
	org := dbtest.CreateOrg(t, queries.Queries, "acme")
	user := dbtest.CreateUser(t, queries.Queries, org.ID, handlers.RoleOwner)
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

func TestNewMuxBodyLimit(t *testing.T) {
	_, queries := dbtest.New(t)
	h, err := handlers.New(queries, "", "", nil, slog.New(slog.DiscardHandler), config.DerpConfig{}, config.AuthConfig{}, config.FunnelConfig{}, nil)
	if err != nil {
		t.Fatalf("handlers.New: %v", err)
	}
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	mux := NewMux(h, requireAuthAPI, slog.New(slog.DiscardHandler), http.NotFoundHandler(), nil)

	auth.Setup("test-session-secret-at-least-32-bytes-long")
	org := dbtest.CreateOrg(t, queries.Queries, "acme")
	user := dbtest.CreateUser(t, queries.Queries, org.ID, handlers.RoleOwner)
	storeRec := httptest.NewRecorder()
	if err := auth.StoreUserSession(storeRec, httptest.NewRequest(http.MethodGet, "/", nil), user.ID); err != nil {
		t.Fatalf("StoreUserSession: %v", err)
	}

	tests := []struct {
		name    string
		content int
		want    int
	}{
		{"under limit", 1024, http.StatusOK},
		{"over limit", maxConsoleBodyBytes, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"content": "\"" + string(bytes.Repeat([]byte("a"), tt.content)) + "\""})
			req := httptest.NewRequest(http.MethodPost, "/api/hujson/format", bytes.NewReader(body))
			for _, c := range storeRec.Result().Cookies() {
				req.AddCookie(c)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestNewMuxCrossOriginProtection(t *testing.T) {
	mux := newTestMux(t)

	tests := []struct {
		name    string
		method  string
		headers map[string]string
		forbid  bool
	}{
		{"same-site post", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site"}, true},
		{"cross-site post", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, true},
		{"sibling origin post", http.MethodPost, map[string]string{"Origin": "https://acme.example.com"}, true},
		{"same-origin post", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin"}, false},
		{"non-browser post", http.MethodPost, nil, false},
		{"same-site get", http.MethodGet, map[string]string{"Sec-Fetch-Site": "same-site"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "https://example.com/api/logout", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if got := rec.Code == http.StatusForbidden; got != tt.forbid {
				t.Errorf("status = %d, forbidden = %v, want %v", rec.Code, got, tt.forbid)
			}
		})
	}
}

// TestNewMuxOverHTTP builds the same mux cmd/ownscale/main.go does, backed
// by a real database, and drives it via httptest.NewServer over real HTTP
// (not the httptest.NewRecorder used by the other tests here). It bypasses real Google OAuth (not practical to
// exercise in CI) by seeding a user and minting its session cookie directly
// via auth.StoreUserSession.
func TestNewMuxOverHTTP(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	_, queries := dbtest.New(t)

	const baseDomain = "srvit.test"

	h, err := handlers.New(queries, baseDomain, t.TempDir(), nil, logger, config.DerpConfig{}, config.AuthConfig{}, config.FunnelConfig{Domain: baseDomain, MaxBodyBytes: 1 << 20}, nil)
	if err != nil {
		t.Fatalf("handlers.New: %v", err)
	}
	auth.Setup("test-session-secret-at-least-32-bytes-long")
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(queries.Queries, next)
	}
	mux := NewMux(h, requireAuthAPI, logger, http.NotFoundHandler(), nil)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// Seed an org (no headscale started; these are HTTP-layer tests) and a
	// user, and mint its session cookie directly, bypassing the real OAuth
	// redirect flow.
	org := dbtest.CreateOrg(t, queries.Queries, "srvittest")
	user := dbtest.CreateUser(t, queries.Queries, org.ID, handlers.RoleOwner)
	storeRec := httptest.NewRecorder()
	if err := auth.StoreUserSession(storeRec, httptest.NewRequest(http.MethodGet, "/", nil), user.ID); err != nil {
		t.Fatalf("StoreUserSession: %v", err)
	}
	sessionCookies := storeRec.Result().Cookies()

	authedRequest := func(t *testing.T, method, path string, body []byte) *http.Response {
		t.Helper()
		var reqBody io.Reader
		if body != nil {
			reqBody = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, ts.URL+path, reqBody)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		for _, c := range sessionCookies {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	t.Run("GET /api/session returns the seeded user", func(t *testing.T) {
		resp := authedRequest(t, http.MethodGet, "/api/session", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var body struct {
			User sqlcgen.User `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.User.ID != user.ID {
			t.Errorf("session user id = %q, want %q", body.User.ID, user.ID)
		}
	})

	t.Run("POST /api/logout clears the session", func(t *testing.T) {
		logoutResp := authedRequest(t, http.MethodPost, "/api/logout", nil)
		defer logoutResp.Body.Close()
		if logoutResp.StatusCode != http.StatusNoContent {
			t.Fatalf("logout status = %d, want %d", logoutResp.StatusCode, http.StatusNoContent)
		}

		// Replace the session cookies with whatever logout just set
		// (cleared/expired), then confirm a follow-up request 401s.
		sessionCookies = logoutResp.Cookies()
		afterResp := authedRequest(t, http.MethodGet, "/api/session", nil)
		defer afterResp.Body.Close()
		if afterResp.StatusCode != http.StatusUnauthorized {
			t.Errorf("session status after logout = %d, want %d", afterResp.StatusCode, http.StatusUnauthorized)
		}
	})
}
