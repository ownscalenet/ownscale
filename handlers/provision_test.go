package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/orgs"
)

// TestSleepingOrganizationWakes checks that headscale-backed endpoints wake
// an organization whose headscale was put to sleep.
func TestSleepingOrganizationWakes(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*Handlers) http.HandlerFunc
	}{
		{"list machines", func(h *Handlers) http.HandlerFunc { return h.RequireHeadscaleClient(h.ListMachines) }},
		{"list auth keys", func(h *Handlers) http.HandlerFunc { return h.RequireHeadscaleClient(h.ListAuthKeys) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, queries := newTestHandlers(t)
			org, err := h.orgs.Create(t.Context(), "acme")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			owner := dbtest.CreateUser(t, queries.Queries, org.ID, RoleOwner)
			if err := h.orgs.ForOrg(org).Sleep(t.Context()); err != nil {
				t.Fatalf("Sleep: %v", err)
			}

			req := withTestUser(httptest.NewRequest(http.MethodGet, "/", nil), owner)
			rec := httptest.NewRecorder()
			tt.handler(h)(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
			}
			if got := h.orgs.Status(org.Subdomain); got != orgs.StatusActive {
				t.Errorf("Status = %q, want %q", got, orgs.StatusActive)
			}
		})
	}
}
