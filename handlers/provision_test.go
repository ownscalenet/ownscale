package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
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
// key starts the organization's headscale and links the member to the
// headscale user with their email.
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
			h, _, users := newOrgTestHandlers(t, RoleOwner, RoleMember)
			owner, member := users[0], users[1]
			// newFakeHeadscaleServer has headscale user "1" for alice@example.com.
			if err := h.queries.UpdateUserProfile(t.Context(), sqlcgen.UpdateUserProfileParams{
				Email: "alice@example.com", Name: member.Name, AvatarUrl: member.AvatarUrl,
				EmailVerified: member.EmailVerified, ID: member.ID,
			}); err != nil {
				t.Fatalf("UpdateUserProfile: %v", err)
			}
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
			got, err := h.queries.GetUserByID(t.Context(), member.ID)
			if err != nil {
				t.Fatalf("GetUserByID: %v", err)
			}
			if got.HeadscaleUserID.String != "1" {
				t.Errorf("member's headscale user = %+v, want 1", got.HeadscaleUserID)
			}
		})
	}
}

// TestStartLinksMembers checks that starting an organization's headscale
// links its members to headscale users, so their first OIDC login through
// headscale doesn't create a second one.
func TestStartLinksMembers(t *testing.T) {
	h, queries := newTestHandlers(t)
	org, err := h.orgs.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	members := []sqlcgen.User{
		dbtest.CreateUser(t, queries.Queries, org.ID, RoleOwner),
		dbtest.CreateUser(t, queries.Queries, org.ID, RoleMember),
	}

	client, err := h.orgs.Ensure(t.Context(), org.Subdomain)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	hsUsers, err := client.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	emails := map[string]string{}
	for _, u := range hsUsers {
		emails[u.ID] = u.Email
	}
	for _, m := range members {
		got, err := queries.GetUserByID(t.Context(), m.ID)
		if err != nil {
			t.Fatalf("GetUserByID: %v", err)
		}
		if !got.HeadscaleUserID.Valid || emails[got.HeadscaleUserID.String] != m.Email {
			t.Errorf("member %s linked to %+v, want a headscale user with email %s", m.Email, got.HeadscaleUserID, m.Email)
		}
	}
}
