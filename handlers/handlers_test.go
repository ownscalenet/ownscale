package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/utils"
)

// TestSessionOmitsHeadscaleUserID checks that the session response leaves
// out the user's headscale user, which is internal.
func TestSessionOmitsHeadscaleUserID(t *testing.T) {
	h, queries := newTestHandlers(t)
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req = authenticatedRequest(t, req, user.ID)
	rec := httptest.NewRecorder()
	auth.RequireAuthAPI(queries.Queries, h.Session)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		User map[string]any `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if v, ok := body.User["HeadscaleUserID"]; ok {
		t.Errorf("session user has HeadscaleUserID = %v, want it omitted", v)
	}
}

func TestSessionAuthenticated(t *testing.T) {
	h, queries := newTestHandlers(t)
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req = authenticatedRequest(t, req, user.ID)

	rec := httptest.NewRecorder()
	auth.RequireAuthAPI(queries.Queries, h.Session)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	for _, want := range []string{user.ID, `"version":"` + utils.Version + `"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", rec.Body.String(), want)
		}
	}
}

func TestSessionUnauthenticated(t *testing.T) {
	h, queries := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rec := httptest.NewRecorder()
	auth.RequireAuthAPI(queries.Queries, h.Session)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWriteJSON(t *testing.T) {
	h, _ := newTestHandlers(t)

	rec := httptest.NewRecorder()
	h.writeJSON(rec, http.StatusCreated, map[string]string{"ok": "yes"})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	if !strings.Contains(rec.Body.String(), `"ok":"yes"`) {
		t.Errorf("body = %q, want it to contain the encoded value", rec.Body.String())
	}
}

func TestWriteJSONNilBody(t *testing.T) {
	h, _ := newTestHandlers(t)

	rec := httptest.NewRecorder()
	h.writeJSON(rec, http.StatusNoContent, nil)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestWriteError(t *testing.T) {
	h, _ := newTestHandlers(t)

	rec := httptest.NewRecorder()
	h.writeError(rec, http.StatusBadRequest, "bad input")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "bad input") {
		t.Errorf("body = %q, want it to contain the error message", rec.Body.String())
	}
}
