package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// newMembersTestHandlers builds Handlers (no organization provisioning
// configured, mirroring newMachinesTestHandlers) and an active organization,
// inserting one user per role in roles (in order). It returns the handlers,
// queries, and the created users.
func newMembersTestHandlers(t *testing.T, roles ...string) (*Handlers, *dbpkg.TxQueries, []sqlcgen.User) {
	t.Helper()
	queries := newTestQueries(t)
	h := New(queries, nil, "", nil, discardLogger(), config.DerpConfig{})

	orgID := uuid.Must(uuid.NewV7()).String()
	org, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{ID: orgID, Subdomain: "acme"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if _, err := queries.UpdateOrganizationStatus(t.Context(), sqlcgen.UpdateOrganizationStatusParams{ID: org.ID, Status: "active"}); err != nil {
		t.Fatalf("UpdateOrganizationStatus: %v", err)
	}

	users := make([]sqlcgen.User, 0, len(roles))
	for _, role := range roles {
		u := insertUser(t, queries)
		if err := queries.SetUserOrganizationAndRole(t.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
			OrganizationID: sql.NullString{String: org.ID, Valid: true},
			Role:           role,
			ID:             u.ID,
		}); err != nil {
			t.Fatalf("SetUserOrganizationAndRole: %v", err)
		}
		u, err = queries.GetUserByID(t.Context(), u.ID)
		if err != nil {
			t.Fatalf("GetUserByID: %v", err)
		}
		users = append(users, u)
	}
	return h, queries, users
}

func TestListMembers(t *testing.T) {
	h, _, users := newMembersTestHandlers(t, RoleOwner, RoleMember)
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
	h, _, users := newMembersTestHandlers(t, RoleMember, RoleMember)
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
	h, queries, users := newMembersTestHandlers(t, RoleOwner, RoleMember)
	owner, target := users[0], users[1]

	body := strings.NewReader(`{"role":"admin"}`)
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/x/role", body), owner), "id", target.ID)
	rec := httptest.NewRecorder()
	h.UpdateMemberRole(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	got, err := queries.GetUserByID(t.Context(), target.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", got.Role)
	}
}

func TestUpdateMemberRoleRejectsOwnerTransitions(t *testing.T) {
	h, _, users := newMembersTestHandlers(t, RoleOwner, RoleMember)
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

func TestListMembersAutoLinksByEmail(t *testing.T) {
	h, queries, users := newMembersTestHandlers(t, RoleOwner, RoleMember)
	owner, target := users[0], users[1]

	// Give the member the email the fake headscale server's "alice" user
	// carries (see newFakeHeadscaleServer in machines_test.go), so
	// ListMembers' auto-link-by-email logic finds a match.
	if err := queries.UpdateUserProfile(t.Context(), sqlcgen.UpdateUserProfileParams{
		Email:         "alice@example.com",
		Name:          target.Name,
		AvatarUrl:     target.AvatarUrl,
		EmailVerified: target.EmailVerified,
		ID:            target.ID,
	}); err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}

	_, client, _ := newFakeHeadscaleServer(t)
	h.hs = &fakeMachinesRegistry{client: client}

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/members", nil), owner)
	rec := httptest.NewRecorder()
	h.ListMembers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	got, err := queries.GetUserByID(t.Context(), target.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if !got.HeadscaleUserID.Valid || got.HeadscaleUserID.String != "1" {
		t.Errorf("headscaleUserId = %+v, want linked to headscale user 1 (alice)", got.HeadscaleUserID)
	}

	// The link is purely internal bookkeeping -- the JSON response must
	// not surface it.
	if strings.Contains(rec.Body.String(), "headscaleUserId") {
		t.Errorf("response leaks headscaleUserId: %s", rec.Body)
	}
}

func TestListMembersDoesNotRelinkAlreadyLinkedMember(t *testing.T) {
	h, queries, users := newMembersTestHandlers(t, RoleOwner, RoleMember)
	owner, target := users[0], users[1]

	if err := queries.UpdateUserProfile(t.Context(), sqlcgen.UpdateUserProfileParams{
		Email:         "alice@example.com",
		Name:          target.Name,
		AvatarUrl:     target.AvatarUrl,
		EmailVerified: target.EmailVerified,
		ID:            target.ID,
	}); err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}
	// Pre-link to a different headscale user than the one email-matching
	// would pick ("1"/alice) -- ListMembers must leave this alone.
	if err := queries.LinkUserHeadscaleUser(t.Context(), sqlcgen.LinkUserHeadscaleUserParams{
		HeadscaleUserID: sql.NullString{String: "2", Valid: true},
		ID:              target.ID,
	}); err != nil {
		t.Fatalf("LinkUserHeadscaleUser: %v", err)
	}

	_, client, _ := newFakeHeadscaleServer(t)
	h.hs = &fakeMachinesRegistry{client: client}

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/members", nil), owner)
	rec := httptest.NewRecorder()
	h.ListMembers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	got, err := queries.GetUserByID(t.Context(), target.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.HeadscaleUserID.String != "2" {
		t.Errorf("headscaleUserId = %+v, want unchanged at 2", got.HeadscaleUserID)
	}
}

func TestRemoveMember(t *testing.T) {
	h, queries, users := newMembersTestHandlers(t, RoleOwner, RoleMember)
	owner, target := users[0], users[1]

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/members/x", nil), owner), "id", target.ID)
	rec := httptest.NewRecorder()
	h.RemoveMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	got, err := queries.GetUserByID(t.Context(), target.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.OrganizationID.Valid {
		t.Errorf("expected organization to be cleared, got %+v", got.OrganizationID)
	}
}

func TestRemoveMemberRefusesOwner(t *testing.T) {
	h, _, users := newMembersTestHandlers(t, RoleOwner, RoleAdmin)
	owner, admin := users[0], users[1]

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/members/x", nil), admin), "id", owner.ID)
	rec := httptest.NewRecorder()
	h.RemoveMember(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestTransferOwnership(t *testing.T) {
	h, queries, users := newMembersTestHandlers(t, RoleOwner, RoleAdmin)
	owner, admin := users[0], users[1]

	body := strings.NewReader(`{"memberId":"` + admin.ID + `"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/transfer-ownership", body), owner)
	rec := httptest.NewRecorder()
	h.TransferOwnership(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	newOwner, err := queries.GetUserByID(t.Context(), admin.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if newOwner.Role != RoleOwner {
		t.Errorf("new owner role = %q, want owner", newOwner.Role)
	}
	oldOwner, err := queries.GetUserByID(t.Context(), owner.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if oldOwner.Role != RoleAdmin {
		t.Errorf("old owner role = %q, want admin", oldOwner.Role)
	}
}

func TestTransferOwnershipRequiresOwner(t *testing.T) {
	h, _, users := newMembersTestHandlers(t, RoleAdmin, RoleMember)
	admin, target := users[0], users[1]

	body := strings.NewReader(`{"memberId":"` + target.ID + `"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/members/transfer-ownership", body), admin)
	rec := httptest.NewRecorder()
	h.TransferOwnership(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
}
