package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

func TestGetAuthRestrictionsDefaultsToEmpty(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleMember)
	member := users[0]

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), member)
	rec := httptest.NewRecorder()
	h.GetAuthRestrictions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Content   string `json:"content"`
		CanManage bool   `json:"canManage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.CanManage {
		t.Error("canManage = true for a plain member, want false")
	}
	if _, err := hujson.Parse([]byte(resp.Content)); err != nil {
		t.Errorf("default content isn't HuJSON: %v", err)
	}
}

func TestUpdateAuthRestrictionsRequiresManager(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleMember)
	member := users[0]

	body := `{"content":"{\"domains\":[\"example.com\"]}"}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), member)
	rec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateAuthRestrictionsRejectsInvalidHuJSON(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	body := `{"content":"not valid hujson {{{"}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), owner)
	rec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdateAuthRestrictionsPersistsAndReturnsOK(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	newContent := `{"domains":["example.com"],"groups":["group:eng"],"users":["alice@example.com"]}`
	body := `{"content":` + jsonQuote(newContent) + `}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), owner)
	rec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	getReq := withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), owner)
	getRec := httptest.NewRecorder()
	h.GetAuthRestrictions(getRec, getReq)

	var resp struct {
		Content   string `json:"content"`
		CanManage bool   `json:"canManage"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Content != newContent {
		t.Errorf("content = %q, want %q", resp.Content, newContent)
	}
	if !resp.CanManage {
		t.Error("canManage = false for an owner, want true")
	}
}
