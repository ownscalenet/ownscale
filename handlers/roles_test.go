package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
)

// callerWithRole returns a user of owner's organization with role, linked
// to headscale user hsUserID. In newFakeHeadscaleServer, headscale user
// "1" (alice) owns node "1" and pre-auth key "1"; user "2" owns neither.
func callerWithRole(t *testing.T, h *Handlers, owner sqlcgen.User, role, hsUserID string) sqlcgen.User {
	t.Helper()
	user := dbtest.CreateUser(t, h.queries.Queries, owner.OrganizationID, role)
	if err := h.queries.LinkUserHeadscaleUser(t.Context(), sqlcgen.LinkUserHeadscaleUserParams{
		HeadscaleUserID: sql.NullString{String: hsUserID, Valid: true},
		ID:              user.ID,
	}); err != nil {
		t.Fatalf("LinkUserHeadscaleUser: %v", err)
	}
	user.HeadscaleUserID = sql.NullString{String: hsUserID, Valid: true}
	return user
}

func TestRoleAccessToMachinesAndKeys(t *testing.T) {
	const registerKey = "hskey-authreq-abcdefghijklmnopqrstuvwx"
	type endpoint struct {
		handler func(*Handlers) http.HandlerFunc
		method  string
		id      string
		body    string
	}
	var (
		rename = endpoint{func(h *Handlers) http.HandlerFunc { return h.RenameMachine }, http.MethodPost, "1", `{"name":"laptop"}`}
		expire = endpoint{func(h *Handlers) http.HandlerFunc { return h.ExpireMachine }, http.MethodPost, "1", ""}
		del    = endpoint{func(h *Handlers) http.HandlerFunc { return h.DeleteMachine }, http.MethodDelete, "1", ""}
		tags   = endpoint{func(h *Handlers) http.HandlerFunc { return h.SetMachineTags }, http.MethodPost, "1", `{"tags":["tag:server"]}`}
		routes = endpoint{func(h *Handlers) http.HandlerFunc { return h.SetMachineRoutes }, http.MethodPost, "1", `{"routes":["10.0.0.0/24"]}`}
		reg    = func(member string) endpoint {
			return endpoint{func(h *Handlers) http.HandlerFunc { return h.RegisterMachine }, http.MethodPost, "", `{"memberId":"` + member + `","key":"` + registerKey + `"}`}
		}
		createKey = func(body string) endpoint {
			return endpoint{func(h *Handlers) http.HandlerFunc { return h.CreateAuthKey }, http.MethodPost, "", body}
		}
		expireKey = endpoint{func(h *Handlers) http.HandlerFunc { return h.ExpireAuthKey }, http.MethodPost, "1", ""}
		deleteKey = endpoint{func(h *Handlers) http.HandlerFunc { return h.DeleteAuthKey }, http.MethodDelete, "1", ""}
	)

	tests := []struct {
		name       string
		role       string
		hsUserID   string
		endpoint   endpoint
		wantStatus int
	}{
		{"member renames own machine", RoleMember, "1", rename, http.StatusOK},
		{"member renames other's machine", RoleMember, "2", rename, http.StatusForbidden},
		{"admin renames other's machine", RoleAdmin, "2", rename, http.StatusOK},
		{"member expires own machine", RoleMember, "1", expire, http.StatusOK},
		{"member expires other's machine", RoleMember, "2", expire, http.StatusForbidden},
		{"member deletes own machine", RoleMember, "1", del, http.StatusOK},
		{"member deletes other's machine", RoleMember, "2", del, http.StatusForbidden},
		{"network admin deletes other's machine", RoleNetworkAdmin, "2", del, http.StatusForbidden},
		{"member tags own machine", RoleMember, "1", tags, http.StatusForbidden},
		{"admin tags machine", RoleAdmin, "2", tags, http.StatusOK},
		{"member approves routes on own machine", RoleMember, "1", routes, http.StatusForbidden},
		{"admin approves routes", RoleAdmin, "2", routes, http.StatusOK},
		{"member registers machine for self", RoleMember, "1", reg("{self}"), http.StatusOK},
		{"member registers machine for other", RoleMember, "1", reg("{other}"), http.StatusForbidden},
		{"admin registers machine for other", RoleAdmin, "1", reg("{other}"), http.StatusOK},
		{"admin registers machine for non-member", RoleAdmin, "1", reg("nobody"), http.StatusNotFound},
		{"member creates own key", RoleMember, "1", createKey(`{"memberId":"{self}","expiryDays":1}`), http.StatusOK},
		{"member creates tagged key", RoleMember, "1", createKey(`{"memberId":"{self}","aclTags":["tag:server"],"expiryDays":1}`), http.StatusForbidden},
		{"member creates key for other", RoleMember, "2", createKey(`{"memberId":"{other}","expiryDays":1}`), http.StatusForbidden},
		{"admin creates tag-only key", RoleAdmin, "2", createKey(`{"aclTags":["tag:server"],"expiryDays":1}`), http.StatusOK},
		{"member expires own key", RoleMember, "1", expireKey, http.StatusOK},
		{"member expires other's key", RoleMember, "2", expireKey, http.StatusForbidden},
		{"member deletes own key", RoleMember, "1", deleteKey, http.StatusOK},
		{"member deletes other's key", RoleMember, "2", deleteKey, http.StatusForbidden},
		{"admin deletes other's key", RoleAdmin, "2", deleteKey, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, queries := newTestHandlers(t)
			owner, client, _ := activeOrgUser(t, h, queries)
			caller := callerWithRole(t, h, owner, tt.role, tt.hsUserID)
			// other is linked to whichever of "1" and "2" caller isn't.
			other := callerWithRole(t, h, owner, RoleMember, map[string]string{"1": "2", "2": "1"}[tt.hsUserID])
			body := strings.NewReplacer("{self}", caller.ID, "{other}", other.ID).Replace(tt.endpoint.body)
			org, err := queries.GetOrganization(t.Context(), owner.OrganizationID)
			if err != nil {
				t.Fatalf("GetOrganization: %v", err)
			}

			req := httptest.NewRequest(tt.endpoint.method, "/", strings.NewReader(body))
			req = withPathValue(withHeadscaleClientOrg(withTestUser(req, caller), client, org), "id", tt.endpoint.id)
			rec := httptest.NewRecorder()
			tt.endpoint.handler(h)(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
		})
	}
}

func TestListAuthKeysByRole(t *testing.T) {
	tests := []struct {
		name          string
		role          string
		hsUserID      string
		wantKeys      int
		wantCanManage bool
	}{
		{"admin sees all keys", RoleAdmin, "2", 1, true},
		{"member sees own keys", RoleMember, "1", 1, false},
		{"member doesn't see other's keys", RoleMember, "2", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, queries := newTestHandlers(t)
			owner, client, _ := activeOrgUser(t, h, queries)
			caller := callerWithRole(t, h, owner, tt.role, tt.hsUserID)

			req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-keys", nil), caller), client)
			rec := httptest.NewRecorder()
			h.ListAuthKeys(rec, req)

			var body struct {
				AuthKeys  []hscl.PreAuthKey `json:"authKeys"`
				CanManage bool              `json:"canManage"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(body.AuthKeys) != tt.wantKeys || body.CanManage != tt.wantCanManage {
				t.Errorf("got %d keys, canManage %v; want %d, %v", len(body.AuthKeys), body.CanManage, tt.wantKeys, tt.wantCanManage)
			}
		})
	}
}
