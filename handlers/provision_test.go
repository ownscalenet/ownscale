package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/orgs"
)

// TestUnprovisionedOrganization checks what the headscale-backed endpoints
// answer before the organization's headscale first started: empty results
// for reads, and 404 for acting on something that can't exist yet.
func TestUnprovisionedOrganization(t *testing.T) {
	tests := []struct {
		name       string
		handler    func(*Handlers) http.HandlerFunc
		method     string
		wantStatus int
		// want holds fields the response must have, as decoded JSON.
		want map[string]any
	}{
		{
			name:       "list machines",
			handler:    func(h *Handlers) http.HandlerFunc { return h.ReadHeadscale(h.ListMachines) },
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			want:       map[string]any{"machines": []any{}, "users": []any{}, "total": float64(0)},
		},
		{
			name:       "list auth keys",
			handler:    func(h *Handlers) http.HandlerFunc { return h.ReadHeadscale(h.ListAuthKeys) },
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			want:       map[string]any{"authKeys": []any{}, "users": []any{}},
		},
		{
			name:       "access control policy",
			handler:    func(h *Handlers) http.HandlerFunc { return h.ReadHeadscale(h.GetAccessControlPolicy) },
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			want:       map[string]any{"content": orgs.DefaultACLPolicy},
		},
		{
			name:       "list funnel routes",
			handler:    func(h *Handlers) http.HandlerFunc { return h.ReadHeadscale(h.ListFunnelRoutes) },
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			want:       map[string]any{"funnelRoutes": []any{}},
		},
		{
			name:       "rename machine",
			handler:    func(h *Handlers) http.HandlerFunc { return h.RequireHeadscaleClient(h.RenameMachine) },
			method:     http.MethodPost,
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, users := newOrgTestHandlers(t, RoleOwner)
			hs := &lazyHeadscaleClients{}
			h.hs = hs

			req := withTestUser(httptest.NewRequest(tt.method, "/", strings.NewReader(`{"name":"laptop"}`)), users[0])
			rec := httptest.NewRecorder()
			tt.handler(h)(rec, withPathValue(req, "id", "1"))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if hs.started {
				t.Error("headscale started, want it left for first use")
			}
			var body map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			for k, want := range tt.want {
				if got := body[k]; !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %#v, want %#v", k, got, want)
				}
			}
		})
	}
}

// TestProvisionOnFirstUse checks that registering a machine or creating a
// key starts the organization's headscale.
func TestProvisionOnFirstUse(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*Handlers) http.HandlerFunc
		body    string
	}{
		{
			name:    "register machine",
			handler: func(h *Handlers) http.HandlerFunc { return h.ProvisionHeadscale(h.RegisterMachine) },
			body:    `{"memberId":"{member}","key":"abcdefghijklmnopqrstuvwx"}`,
		},
		{
			name:    "create auth key",
			handler: func(h *Handlers) http.HandlerFunc { return h.ProvisionHeadscale(h.CreateAuthKey) },
			body:    `{"memberId":"{member}","expiryDays":1}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, org, users := newOrgTestHandlers(t, RoleOwner)
			owner := users[0]
			// newFakeHeadscaleServer has headscale user "1".
			member := dbtest.CreateUserWithHeadscaleUser(t, h.queries.Queries, org.ID, RoleMember, "1")
			client, _, _, _ := newFakeHeadscaleServer(t)
			hs := &lazyHeadscaleClients{client: client}
			h.hs = hs

			body := strings.ReplaceAll(tt.body, "{member}", member.ID)
			req := withTestUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), owner)
			rec := httptest.NewRecorder()
			tt.handler(h)(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
			}
			if !hs.started {
				t.Error("headscale not started")
			}
		})
	}
}

// TestSleepingOrganizationWakes checks that headscale-backed endpoints wake
// an organization whose headscale was put to sleep.
func TestSleepingOrganizationWakes(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*Handlers) http.HandlerFunc
	}{
		{"list machines", func(h *Handlers) http.HandlerFunc { return h.ReadHeadscale(h.ListMachines) }},
		{"list auth keys", func(h *Handlers) http.HandlerFunc { return h.ReadHeadscale(h.ListAuthKeys) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, org, users := newOrgTestHandlers(t, RoleOwner)
			if _, err := h.orgs.Ensure(t.Context(), org.Subdomain); err != nil {
				t.Fatalf("Ensure: %v", err)
			}
			if err := h.orgs.ForOrg(org).Sleep(t.Context()); err != nil {
				t.Fatalf("Sleep: %v", err)
			}

			req := withTestUser(httptest.NewRequest(http.MethodGet, "/", nil), users[0])
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
