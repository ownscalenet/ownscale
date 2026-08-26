package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/auth"
)

func TestSessionAuthenticated(t *testing.T) {
	h, queries := newTestHandlers(t, false)
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req = authenticatedRequest(t, req, user.ID)

	rec := httptest.NewRecorder()
	auth.RequireAuthAPI(queries.Queries, h.Session)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), user.ID) {
		t.Errorf("body = %q, want it to contain user id %q", rec.Body.String(), user.ID)
	}
}

func TestSessionUnauthenticated(t *testing.T) {
	h, queries := newTestHandlers(t, false)

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rec := httptest.NewRecorder()
	auth.RequireAuthAPI(queries.Queries, h.Session)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWriteJSON(t *testing.T) {
	h, _ := newTestHandlers(t, false)

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
	h, _ := newTestHandlers(t, false)

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
	h, _ := newTestHandlers(t, false)

	rec := httptest.NewRecorder()
	h.writeError(rec, http.StatusBadRequest, "bad input")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "bad input") {
		t.Errorf("body = %q, want it to contain the error message", rec.Body.String())
	}
}
