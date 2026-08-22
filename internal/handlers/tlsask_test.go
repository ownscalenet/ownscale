package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
)

func TestTLSAsk(t *testing.T) {
	h, queries := newTestHandlers(t, false)
	h.baseDomain = testBaseDomain

	if _, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{
		ID:        uuid.Must(uuid.NewV7()).String(),
		Subdomain: "acme",
	}); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	tests := []struct {
		name   string
		domain string
		want   int
	}{
		{"missing domain", "", http.StatusForbidden},
		{"base domain", testBaseDomain, http.StatusOK},
		{"admin subdomain", "admin." + testBaseDomain, http.StatusOK},
		{"known org subdomain", "acme." + testBaseDomain, http.StatusOK},
		{"unknown subdomain", "ghost." + testBaseDomain, http.StatusForbidden},
		{"extra dot in subdomain", "a.b." + testBaseDomain, http.StatusForbidden},
		{"unrelated domain", "evil.com", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/tls-ask?domain="+tt.domain, nil)
			rec := httptest.NewRecorder()
			h.TLSAsk(rec, req)
			if rec.Code != tt.want {
				t.Errorf("domain %q: status = %d, want %d", tt.domain, rec.Code, tt.want)
			}
		})
	}
}

func TestTLSAskNoBaseDomainConfigured(t *testing.T) {
	h, _ := newTestHandlers(t, false)
	// baseDomain left empty (as when running without provisioning configured).

	req := httptest.NewRequest(http.MethodGet, "/api/tls-ask?domain=anything.example.com", nil)
	rec := httptest.NewRecorder()
	h.TLSAsk(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
