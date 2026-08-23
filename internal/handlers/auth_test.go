package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogout(t *testing.T) {
	h, queries := newTestHandlers(t, false)
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
	h, queries := newTestHandlers(t, false)
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req = authenticatedRequest(t, req, user.ID)

	rec := httptest.NewRecorder()
	h.APILogout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
