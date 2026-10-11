package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

func TestCreateInviteRequiresManager(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleMember)
	member := users[0]

	body := strings.NewReader(`{"role":"member"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/invites", body), member)
	rec := httptest.NewRecorder()
	h.CreateInvite(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestCreateAndListInvites(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	body := strings.NewReader(`{"email":"new@example.com","role":"admin"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/invites", body), owner)
	rec := httptest.NewRecorder()
	h.CreateInvite(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var created struct {
		Invite invite `json:"invite"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Invite.Token == "" {
		t.Fatal("expected a non-empty token")
	}

	listReq := withTestUser(httptest.NewRequest(http.MethodGet, "/api/invites", nil), owner)
	listRec := httptest.NewRecorder()
	h.ListInvites(listRec, listReq)
	var listed struct {
		Invites []invite `json:"invites"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Invites) != 1 || listed.Invites[0].Token != created.Invite.Token {
		t.Fatalf("unexpected invites: %+v", listed.Invites)
	}
}

func TestCreateInviteRejectsOwnerRole(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	body := strings.NewReader(`{"role":"owner"}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/invites", body), owner)
	rec := httptest.NewRecorder()
	h.CreateInvite(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestGetInvitePreview(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	inv, err := h.queries.CreateInvite(t.Context(), sqlcgen.CreateInviteParams{
		ID:             xid.New().String(),
		OrganizationID: owner.OrganizationID,
		Role:           RoleMember,
		Token:          "test-token",
		CreatedBy:      owner.ID,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	req := withPathValue(httptest.NewRequest(http.MethodGet, "/api/invites/test-token", nil), "token", inv.Token)
	rec := httptest.NewRecorder()
	h.GetInvitePreview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body struct {
		Valid              bool   `json:"valid"`
		InvitedBy          string `json:"invitedBy"`
		OrganizationDomain string `json:"organizationDomain"`
		Role               string `json:"role"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Valid {
		t.Error("expected valid=true")
	}
	if body.InvitedBy != owner.Name {
		t.Errorf("invitedBy = %q, want %q", body.InvitedBy, owner.Name)
	}
	if !strings.HasSuffix(body.OrganizationDomain, "."+h.baseDomain) {
		t.Errorf("organizationDomain = %q, want suffix %q", body.OrganizationDomain, "."+h.baseDomain)
	}
	if body.Role != RoleMember {
		t.Errorf("role = %q, want %q", body.Role, RoleMember)
	}
}

func TestGetInvitePreviewExpired(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	inv, err := h.queries.CreateInvite(t.Context(), sqlcgen.CreateInviteParams{
		ID:             xid.New().String(),
		OrganizationID: owner.OrganizationID,
		Role:           RoleMember,
		Token:          "expired-token",
		CreatedBy:      owner.ID,
		ExpiresAt:      time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	req := withPathValue(httptest.NewRequest(http.MethodGet, "/api/invites/expired-token", nil), "token", inv.Token)
	rec := httptest.NewRecorder()
	h.GetInvitePreview(rec, req)

	var body struct {
		Valid bool `json:"valid"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Valid {
		t.Error("expected valid=false for an expired invite")
	}
}

func TestDeleteInvite(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	inv, err := h.queries.CreateInvite(t.Context(), sqlcgen.CreateInviteParams{
		ID:             xid.New().String(),
		OrganizationID: owner.OrganizationID,
		Role:           RoleMember,
		Token:          "delete-me",
		CreatedBy:      owner.ID,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/invites/"+inv.ID, nil), owner), "id", inv.ID)
	rec := httptest.NewRecorder()
	h.DeleteInvite(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	if _, err := h.queries.GetInvite(t.Context(), inv.ID); err == nil {
		t.Error("expected invite to be deleted")
	}
}
