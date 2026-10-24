package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/hscl/hstest"
)

func TestListMembers(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner, RoleMember)
	owner, plainMember := users[0], users[1]

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/members", nil), owner)
	rec := httptest.NewRecorder()
	h.ListMembers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body struct {
		Members   []member `json:"members"`
		CanManage bool     `json:"canManage"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.CanManage {
		t.Error("expected canManage=true for owner")
	}
	if len(body.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(body.Members))
	}

	req2 := withTestUser(httptest.NewRequest(http.MethodGet, "/api/members", nil), plainMember)
	rec2 := httptest.NewRecorder()
	h.ListMembers(rec2, req2)
	var body2 struct {
		CanManage bool `json:"canManage"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body2.CanManage {
		t.Error("expected canManage=false for plain member")
	}
}

func TestUpdateMemberRoleRequiresManager(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleMember, RoleMember)
	caller, target := users[0], users[1]

	body := strings.NewReader(`{"role":"admin"}`)
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/x/role", body), caller), "id", target.ID)
	rec := httptest.NewRecorder()
	h.UpdateMemberRole(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestUpdateMemberRoleByOwner(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner, RoleMember)
	owner, target := users[0], users[1]

	body := strings.NewReader(`{"role":"admin"}`)
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/x/role", body), owner), "id", target.ID)
	rec := httptest.NewRecorder()
	h.UpdateMemberRole(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	got, err := h.queries.GetUserByID(t.Context(), target.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", got.Role)
	}
}

func TestUpdateMemberRoleRejectsOwnerTransitions(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner, RoleMember)
	owner, target := users[0], users[1]

	// Can't grant owner via this endpoint.
	body := strings.NewReader(`{"role":"owner"}`)
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/x/role", body), owner), "id", target.ID)
	rec := httptest.NewRecorder()
	h.UpdateMemberRole(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("granting owner: status = %d, want 400: %s", rec.Code, rec.Body)
	}

	// Can't change the current owner's role via this endpoint either.
	body2 := strings.NewReader(`{"role":"admin"}`)
	req2 := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/x/role", body2), owner), "id", owner.ID)
	rec2 := httptest.NewRecorder()
	h.UpdateMemberRole(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("demoting owner: status = %d, want 400: %s", rec2.Code, rec2.Body)
	}
}

// TestListMembersOmitsHeadscaleUserID checks that the members response
// leaves out each member's headscale user, which is internal.
func TestListMembersOmitsHeadscaleUserID(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner, RoleMember)

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/members", nil), users[0])
	rec := httptest.NewRecorder()
	h.ListMembers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); strings.Contains(body, "headscaleUserId") || strings.Contains(body, users[1].HeadscaleUserID) {
		t.Errorf("response leaks the headscale user: %s", body)
	}
}

func TestRemoveMember(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner, RoleMember)
	owner, target := users[0], users[1]
	client, _, _, _ := newFakeHeadscaleServer(t)
	h.hs = fakeHeadscaleClients{client: client}

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/members/x", nil), owner), "id", target.ID)
	rec := httptest.NewRecorder()
	h.RequireHeadscaleClient(h.RemoveMember)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if _, err := h.queries.GetUserByID(t.Context(), target.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetUserByID after remove: err = %v, want sql.ErrNoRows (account should be deleted)", err)
	}
}

// headscaleWithUsers starts a real headscale holding a user with one
// "<name>-laptop" machine for each of names, and returns a client for it and
// the users' IDs by name.
func headscaleWithUsers(t *testing.T, names ...string) (*hscl.Client, map[string]string) {
	t.Helper()
	sock := hstest.Headscale(t)
	client := hscl.NewUnix(sock)
	ids := map[string]string{}
	for _, name := range names {
		user, err := client.CreateUser(t.Context(), hscl.CreateUserParams{Name: name})
		if err != nil {
			t.Fatalf("CreateUser(%q): %v", name, err)
		}
		ids[name] = user.ID
		if _, err := client.RegisterNode(t.Context(), name, hstest.PendingNode(t, sock, name, name+"-laptop")); err != nil {
			t.Fatalf("RegisterNode(%q): %v", name, err)
		}
	}
	return client, ids
}

// TestRemoveMemberDeletesHeadscaleUser checks that removing a member also
// removes their headscale user and its machines, and that nothing is
// removed while headscale is unreachable.
func TestRemoveMemberDeletesHeadscaleUser(t *testing.T) {
	tests := []struct {
		name        string
		unreachable bool
		wantStatus  int
		wantUsers   []string
		wantNodes   []string
		wantAccount bool
	}{
		{"headscale reachable", false, http.StatusOK, []string{"other"}, []string{"other-laptop"}, false},
		{"headscale unreachable", true, http.StatusServiceUnavailable, []string{"member", "other"}, []string{"member-laptop", "other-laptop"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, org, users := newOrgTestHandlers(t, RoleOwner)
			owner := users[0]
			client, hsUserIDs := headscaleWithUsers(t, "member", "other")
			target := dbtest.CreateUserWithHeadscaleUser(t, h.queries.Queries, org.ID, RoleMember, hsUserIDs["member"])
			h.hs = fakeHeadscaleClients{client: client}
			if tt.unreachable {
				h.hs = fakeHeadscaleClients{err: errors.New("not running")}
			}

			req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/members/x", nil), owner), "id", target.ID)
			rec := httptest.NewRecorder()
			h.RequireHeadscaleClient(h.RemoveMember)(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			hsUsers, err := client.ListUsers(t.Context())
			if err != nil {
				t.Fatalf("ListUsers: %v", err)
			}
			var gotUsers []string
			for _, u := range hsUsers {
				gotUsers = append(gotUsers, u.Name)
			}
			nodes, err := client.ListNodes(t.Context())
			if err != nil {
				t.Fatalf("ListNodes: %v", err)
			}
			var gotNodes []string
			for _, n := range nodes {
				gotNodes = append(gotNodes, n.Name)
			}
			slices.Sort(gotUsers)
			slices.Sort(gotNodes)
			if !slices.Equal(gotUsers, tt.wantUsers) {
				t.Errorf("headscale users = %v, want %v", gotUsers, tt.wantUsers)
			}
			if !slices.Equal(gotNodes, tt.wantNodes) {
				t.Errorf("headscale nodes = %v, want %v", gotNodes, tt.wantNodes)
			}
			_, err = h.queries.GetUserByID(t.Context(), target.ID)
			if gotAccount := err == nil; gotAccount != tt.wantAccount {
				t.Errorf("account exists = %v, want %v (err %v)", gotAccount, tt.wantAccount, err)
			}
		})
	}
}

func TestRemoveMemberRefusesOwner(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner, RoleAdmin)
	h.hs = fakeHeadscaleClients{}
	owner, admin := users[0], users[1]

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/members/x", nil), admin), "id", owner.ID)
	rec := httptest.NewRecorder()
	h.RequireHeadscaleClient(h.RemoveMember)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestRemoveMemberRequiresManager(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleMember, RoleMember)
	h.hs = fakeHeadscaleClients{}
	plainMember, target := users[0], users[1]

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/members/x", nil), plainMember), "id", target.ID)
	rec := httptest.NewRecorder()
	h.RequireHeadscaleClient(h.RemoveMember)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
	if _, err := h.queries.GetUserByID(t.Context(), target.ID); err != nil {
		t.Errorf("GetUserByID after forbidden remove: err = %v, want member to still exist", err)
	}
}

func TestRemoveMemberNotFound(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	h.hs = fakeHeadscaleClients{}
	owner := users[0]

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/members/x", nil), owner), "id", "nonexistent-id")
	rec := httptest.NewRecorder()
	h.RequireHeadscaleClient(h.RemoveMember)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestRemoveMemberNotFoundForDifferentOrg(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	h.hs = fakeHeadscaleClients{}
	owner := users[0]
	other := insertUser(t, h.queries)

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/members/x", nil), owner), "id", other.ID)
	rec := httptest.NewRecorder()
	h.RequireHeadscaleClient(h.RemoveMember)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestUpdateMemberRoleUnauthenticated(t *testing.T) {
	h, _, _ := newOrgTestHandlers(t, RoleOwner)

	req := httptest.NewRequest(http.MethodPost, "/api/members/x/role", strings.NewReader(`{"role":"admin"}`))
	rec := httptest.NewRecorder()
	h.UpdateMemberRole(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body)
	}
}

func TestTransferOwnership(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner, RoleAdmin)
	owner, admin := users[0], users[1]

	body := strings.NewReader(`{"memberId":"` + admin.ID + `"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/transfer-ownership", body), owner)
	rec := httptest.NewRecorder()
	h.TransferOwnership(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	newOwner, err := h.queries.GetUserByID(t.Context(), admin.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if newOwner.Role != RoleOwner {
		t.Errorf("new owner role = %q, want owner", newOwner.Role)
	}
	oldOwner, err := h.queries.GetUserByID(t.Context(), owner.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if oldOwner.Role != RoleAdmin {
		t.Errorf("old owner role = %q, want admin", oldOwner.Role)
	}
}

func TestTransferOwnershipRequiresOwner(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleAdmin, RoleMember)
	admin, target := users[0], users[1]

	body := strings.NewReader(`{"memberId":"` + target.ID + `"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/transfer-ownership", body), admin)
	rec := httptest.NewRecorder()
	h.TransferOwnership(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestTransferOwnershipRefusesSelf(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	body := strings.NewReader(`{"memberId":"` + owner.ID + `"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/transfer-ownership", body), owner)
	rec := httptest.NewRecorder()
	h.TransferOwnership(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}

	reloaded, err := h.queries.GetUserByID(t.Context(), owner.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if reloaded.Role != RoleOwner {
		t.Errorf("role = %q, want unchanged owner", reloaded.Role)
	}
}

func TestTransferOwnershipTargetNotFound(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	body := strings.NewReader(`{"memberId":"nonexistent-id"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/transfer-ownership", body), owner)
	rec := httptest.NewRecorder()
	h.TransferOwnership(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
}
