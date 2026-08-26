package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// activeOrgUserWithRole is like activeOrgUser but sets the user's role
// (instead of leaving it at the default "member"), returning the refreshed
// user and the fake headscale server's recorded requests.
func activeOrgUserWithRole(t *testing.T, h *Handlers, queries *dbpkg.TxQueries, role string) sqlcgen.User {
	t.Helper()
	user, _ := activeOrgUser(t, h, queries)
	if err := queries.UpdateUserRole(t.Context(), sqlcgen.UpdateUserRoleParams{Role: role, ID: user.ID}); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
	user, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	return user
}

func TestListHeadscaleUsers(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _ := activeOrgUser(t, h, queries)

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/headscale-users", nil), user)
	rec := httptest.NewRecorder()
	h.ListHeadscaleUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body struct {
		Users []struct {
			Name string `json:"name"`
		} `json:"users"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(body.Users))
	}
}

func TestCreateHeadscaleUserRequiresManager(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := activeOrgUserWithRole(t, h, queries, RoleMember)

	body := strings.NewReader(`{"name":"new-user"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/headscale-users", body), user)
	rec := httptest.NewRecorder()
	h.CreateHeadscaleUser(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestCreateHeadscaleUserByAdmin(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := activeOrgUserWithRole(t, h, queries, RoleAdmin)

	body := strings.NewReader(`{"name":"new-user"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/headscale-users", body), user)
	rec := httptest.NewRecorder()
	h.CreateHeadscaleUser(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
}

func TestRenameHeadscaleUser(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := activeOrgUserWithRole(t, h, queries, RoleOwner)

	body := strings.NewReader(`{"name":"new-name"}`)
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/headscale-users/1/rename", body), user), "id", "1")
	rec := httptest.NewRecorder()
	h.RenameHeadscaleUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
}

func TestDeleteHeadscaleUserBlockedByMachines(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := activeOrgUserWithRole(t, h, queries, RoleOwner)

	// node "1" is owned by headscale user "1" in the fake server's fixture.
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/headscale-users/1", nil), user), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteHeadscaleUser(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
	}
}

func TestDeleteHeadscaleUserWithoutMachines(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := activeOrgUserWithRole(t, h, queries, RoleOwner)

	// headscale user "3" owns no machines in the fake server's fixture.
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/headscale-users/3", nil), user), "id", "3")
	rec := httptest.NewRecorder()
	h.DeleteHeadscaleUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
}
