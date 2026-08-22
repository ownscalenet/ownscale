package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestProvisionOrganizationNotConfigured(t *testing.T) {
	h, _ := newTestHandlers(t, false)

	_, err := h.provisionOrganization(t.Context())
	if err == nil {
		t.Fatal("provisionOrganization: want error when orgs is not configured, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %q, want it to mention 'not configured'", err.Error())
	}
}

func TestProvisionOrganizationSuccess(t *testing.T) {
	h, _ := newTestHandlers(t, true)

	org, err := h.provisionOrganization(t.Context())
	if err != nil {
		t.Fatalf("provisionOrganization: %v", err)
	}
	if org.Subdomain == "" {
		t.Error("provisionOrganization: want a non-empty random subdomain")
	}
}
