package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// newFunnelTestHandlers builds Handlers with a real, active, DB-backed
// organization (via newAccessControlTestHandlers), and one user per role
// in roles.
func newFunnelTestHandlers(t *testing.T, roles ...string) (*Handlers, sqlcgen.Organization, []sqlcgen.User) {
	t.Helper()
	return newAccessControlTestHandlers(t, roles...)
}

func TestCreateFunnelRouteRequiresManager(t *testing.T) {
	h, org, users := newFunnelTestHandlers(t, RoleMember)
	member := users[0]
	_, client, _, _, _ := newFakeHeadscaleServer(t)

	body := `{"label":"web","targetNodeId":"1","targetPort":8080}`
	req := withHeadscaleClient(withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/funnel", strings.NewReader(body)), member), member, org), client)
	rec := httptest.NewRecorder()
	h.CreateFunnelRoute(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestCreateFunnelRouteValidation(t *testing.T) {
	h, org, users := newFunnelTestHandlers(t, RoleOwner)
	owner := users[0]
	_, client, _, _, _ := newFakeHeadscaleServer(t)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"invalid label", `{"label":"Not_Valid!","targetNodeId":"1","targetPort":8080}`, http.StatusBadRequest},
		{"port too low", `{"label":"web","targetNodeId":"1","targetPort":0}`, http.StatusBadRequest},
		{"port too high", `{"label":"web","targetNodeId":"1","targetPort":70000}`, http.StatusBadRequest},
		{"missing target node", `{"label":"web","targetNodeId":"","targetPort":8080}`, http.StatusBadRequest},
		{"unknown target node", `{"label":"web","targetNodeId":"ghost","targetPort":8080}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := withHeadscaleClient(withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/funnel", strings.NewReader(tt.body)), owner), owner, org), client)
			rec := httptest.NewRecorder()
			h.CreateFunnelRoute(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d, body=%s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestCreateFunnelRouteSucceedsAndRejectsDuplicateLabel(t *testing.T) {
	h, org, users := newFunnelTestHandlers(t, RoleOwner)
	owner := users[0]
	_, client, _, _, _ := newFakeHeadscaleServer(t)

	body := `{"label":"web","targetNodeId":"1","targetPort":8080}`
	req := withHeadscaleClient(withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/funnel", strings.NewReader(body)), owner), owner, org), client)
	rec := httptest.NewRecorder()
	h.CreateFunnelRoute(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		FunnelRoute struct {
			ID             string `json:"id"`
			Label          string `json:"label"`
			Hostname       string `json:"hostname"`
			TargetNodeName string `json:"targetNodeName"`
			TargetPort     int64  `json:"targetPort"`
		} `json:"funnelRoute"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantHostname := "web-" + org.Subdomain + "." + testBaseDomain
	if resp.FunnelRoute.Hostname != wantHostname {
		t.Errorf("hostname = %q, want %q", resp.FunnelRoute.Hostname, wantHostname)
	}
	if resp.FunnelRoute.TargetNodeName != "node1" {
		t.Errorf("targetNodeName = %q, want %q", resp.FunnelRoute.TargetNodeName, "node1")
	}

	// Duplicate label for the same org -> 409.
	dupReq := withHeadscaleClient(withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/funnel", strings.NewReader(body)), owner), owner, org), client)
	dupRec := httptest.NewRecorder()
	h.CreateFunnelRoute(dupRec, dupReq)
	if dupRec.Code != http.StatusConflict {
		t.Errorf("duplicate label status = %d, want %d", dupRec.Code, http.StatusConflict)
	}
}

func TestListFunnelRoutesFlagsMissingTargetNode(t *testing.T) {
	h, org, users := newFunnelTestHandlers(t, RoleOwner)
	owner := users[0]
	_, client, _, _, _ := newFakeHeadscaleServer(t)

	if _, err := h.queries.CreateFunnelRoute(t.Context(), sqlcgen.CreateFunnelRouteParams{
		ID:             xid.New().String(),
		OrganizationID: org.ID,
		Label:          "ghost",
		TargetNodeID:   "does-not-exist",
		TargetPort:     8080,
		CreatedBy:      sql.NullString{String: owner.ID, Valid: true},
	}); err != nil {
		t.Fatalf("CreateFunnelRoute: %v", err)
	}

	req := withHeadscaleClient(withActiveOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/api/funnel", nil), owner), owner, org), client)
	rec := httptest.NewRecorder()
	h.ListFunnelRoutes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		FunnelRoutes []struct {
			Label             string `json:"label"`
			TargetNodeMissing bool   `json:"targetNodeMissing"`
		} `json:"funnelRoutes"`
		CanManage bool `json:"canManage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.FunnelRoutes) != 1 || !resp.FunnelRoutes[0].TargetNodeMissing {
		t.Errorf("funnelRoutes = %+v, want one route flagged targetNodeMissing=true", resp.FunnelRoutes)
	}
	if !resp.CanManage {
		t.Error("canManage = false for an owner, want true")
	}
}

func TestDeleteFunnelRouteRequiresManagerAndScopesToOrg(t *testing.T) {
	h, org, users := newFunnelTestHandlers(t, RoleOwner, RoleMember)
	owner, member := users[0], users[1]

	route, err := h.queries.CreateFunnelRoute(t.Context(), sqlcgen.CreateFunnelRouteParams{
		ID:             xid.New().String(),
		OrganizationID: org.ID,
		Label:          "web",
		TargetNodeID:   "1",
		TargetPort:     8080,
		CreatedBy:      sql.NullString{String: owner.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateFunnelRoute: %v", err)
	}

	// A plain member cannot delete.
	memberReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/funnel/"+route.ID, nil), member), member, org)
	memberReq.SetPathValue("id", route.ID)
	memberRec := httptest.NewRecorder()
	h.DeleteFunnelRoute(memberRec, memberReq)
	if memberRec.Code != http.StatusForbidden {
		t.Errorf("member delete status = %d, want %d", memberRec.Code, http.StatusForbidden)
	}

	// Deleting an id from a different org 404s (scoped lookup).
	otherOrg, err := h.orgs.Create(t.Context(), "other")
	if err != nil {
		t.Fatalf("orgs.Create: %v", err)
	}
	otherOwner := insertUser(t, h.queries)
	if err := h.queries.SetUserOrganizationAndRole(t.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
		OrganizationID: otherOrg.ID,
		Role:           RoleOwner,
		ID:             otherOwner.ID,
	}); err != nil {
		t.Fatalf("SetUserOrganizationAndRole: %v", err)
	}
	otherOwner, err = h.queries.GetUserByID(t.Context(), otherOwner.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	wrongOrgReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/funnel/"+route.ID, nil), otherOwner), otherOwner, otherOrg)
	wrongOrgReq.SetPathValue("id", route.ID)
	wrongOrgRec := httptest.NewRecorder()
	h.DeleteFunnelRoute(wrongOrgRec, wrongOrgReq)
	if wrongOrgRec.Code != http.StatusNotFound {
		t.Errorf("wrong-org delete status = %d, want %d", wrongOrgRec.Code, http.StatusNotFound)
	}

	// The owner can delete it.
	ownerReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/funnel/"+route.ID, nil), owner), owner, org)
	ownerReq.SetPathValue("id", route.ID)
	ownerRec := httptest.NewRecorder()
	h.DeleteFunnelRoute(ownerRec, ownerReq)
	if ownerRec.Code != http.StatusOK {
		t.Errorf("owner delete status = %d, want %d, body=%s", ownerRec.Code, http.StatusOK, ownerRec.Body.String())
	}

	if _, err := h.queries.GetFunnelRoute(t.Context(), sqlcgen.GetFunnelRouteParams{ID: route.ID, OrganizationID: org.ID}); err == nil {
		t.Error("GetFunnelRoute after delete: want an error (row should be gone)")
	}
}
