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

func TestCreateFunnelRouteRequiresManager(t *testing.T) {
	h, org, users := newOrgTestHandlers(t, RoleMember)
	member := users[0]
	client, _, _, _ := newFakeHeadscaleServer(t)

	body := `{"label":"web","targetNodeId":"1","targetPort":8080}`
	req := withHeadscaleClientOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/funnel", strings.NewReader(body)), member), client, org)
	rec := httptest.NewRecorder()
	h.CreateFunnelRoute(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestCreateFunnelRouteValidation(t *testing.T) {
	h, org, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]
	client, _, _, _ := newFakeHeadscaleServer(t)

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
			req := withHeadscaleClientOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/funnel", strings.NewReader(tt.body)), owner), client, org)
			rec := httptest.NewRecorder()
			h.CreateFunnelRoute(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d, body=%s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestCreateFunnelRouteRejectsDuplicateLabel(t *testing.T) {
	h, org, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]
	client, _, _, _ := newFakeHeadscaleServer(t)

	if _, err := h.queries.CreateFunnelRoute(t.Context(), sqlcgen.CreateFunnelRouteParams{
		ID:             xid.New().String(),
		OrganizationID: org.ID,
		Label:          "web",
		TargetNodeID:   "1",
		TargetPort:     8080,
		CreatedBy:      sql.NullString{String: owner.ID, Valid: true},
	}); err != nil {
		t.Fatalf("CreateFunnelRoute: %v", err)
	}

	body := `{"label":"web","targetNodeId":"1","targetPort":8080}`
	req := withHeadscaleClientOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/funnel", strings.NewReader(body)), owner), client, org)
	rec := httptest.NewRecorder()
	h.CreateFunnelRoute(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate label status = %d, want %d, body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestListFunnelRoutesFlagsMissingTargetNode(t *testing.T) {
	h, org, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]
	h.contentDomain = "example-funnel.net"
	client, _, _, _ := newFakeHeadscaleServer(t)

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

	req := withHeadscaleClientOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/api/funnel", nil), owner), client, org)
	rec := httptest.NewRecorder()
	h.ListFunnelRoutes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		FunnelRoutes []struct {
			Label             string `json:"label"`
			Hostname          string `json:"hostname"`
			TargetNodeMissing bool   `json:"targetNodeMissing"`
			CreatedBy         string `json:"createdBy"`
		} `json:"funnelRoutes"`
		HostnameSuffix string `json:"hostnameSuffix"`
		CanManage      bool   `json:"canManage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := "-" + org.Subdomain + ".example-funnel.net"; resp.HostnameSuffix != want {
		t.Errorf("hostnameSuffix = %q, want %q", resp.HostnameSuffix, want)
	}
	if len(resp.FunnelRoutes) != 1 || !resp.FunnelRoutes[0].TargetNodeMissing {
		t.Errorf("funnelRoutes = %+v, want one route flagged targetNodeMissing=true", resp.FunnelRoutes)
	}
	if want := "ghost-" + org.Subdomain + ".example-funnel.net"; len(resp.FunnelRoutes) == 1 && resp.FunnelRoutes[0].Hostname != want {
		t.Errorf("hostname = %q, want %q", resp.FunnelRoutes[0].Hostname, want)
	}
	if want := userDisplayName(owner); len(resp.FunnelRoutes) == 1 && resp.FunnelRoutes[0].CreatedBy != want {
		t.Errorf("createdBy = %q, want %q", resp.FunnelRoutes[0].CreatedBy, want)
	}
	if !resp.CanManage {
		t.Error("canManage = false for an owner, want true")
	}
}

func TestDeleteFunnelRouteRequiresManagerAndScopesToOrg(t *testing.T) {
	h, org, users := newOrgTestHandlers(t, RoleOwner, RoleMember)
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
	memberReq := withTestUser(httptest.NewRequest(http.MethodDelete, "/api/funnel/"+route.ID, nil), member)
	memberReq.SetPathValue("id", route.ID)
	memberRec := httptest.NewRecorder()
	h.DeleteFunnelRoute(memberRec, memberReq)
	if memberRec.Code != http.StatusForbidden {
		t.Errorf("member delete status = %d, want %d", memberRec.Code, http.StatusForbidden)
	}

	// Deleting an id from a different org 404s (scoped lookup).
	otherOwner := insertUser(t, h.queries)
	wrongOrgReq := withTestUser(httptest.NewRequest(http.MethodDelete, "/api/funnel/"+route.ID, nil), otherOwner)
	wrongOrgReq.SetPathValue("id", route.ID)
	wrongOrgRec := httptest.NewRecorder()
	h.DeleteFunnelRoute(wrongOrgRec, wrongOrgReq)
	if wrongOrgRec.Code != http.StatusNotFound {
		t.Errorf("wrong-org delete status = %d, want %d", wrongOrgRec.Code, http.StatusNotFound)
	}

	// The owner can delete it.
	ownerReq := withTestUser(httptest.NewRequest(http.MethodDelete, "/api/funnel/"+route.ID, nil), owner)
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
