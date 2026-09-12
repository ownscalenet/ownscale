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
	h, org, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), member), member, org)
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
	content, err := hujson.Parse([]byte(resp.Content))
	if err != nil {
		t.Errorf("couldn't parse: %v", err)
	}
	// For some reason, empty array has a leading space.
	if c := content.Find("/domains").String(); strings.TrimSpace(c) != "[]" {
		t.Errorf("expected domains [], got %v", c)
	}
	if c := content.Find("/groups").String(); strings.TrimSpace(c) != "[]" {
		t.Errorf("expected groups [], got %v", c)
	}
	if c := content.Find("/users").String(); strings.TrimSpace(c) != "[]" {
		t.Errorf("expected users [], got %v", c)
	}
}

func TestUpdateAuthRestrictionsRequiresManager(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	body := `{"content":"{\"domains\":[\"example.com\"]}"}`
	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), member), member, org)
	rec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateAuthRestrictionsRejectsInvalidHuJSON(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	body := `{"content":"not valid hujson {{{"}`
	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), owner), owner, org)
	rec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdateAuthRestrictionsPersistsAndReturnsOK(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	newContent := `{"domains":["example.com"],"groups":["group:eng"],"users":["alice@example.com"]}`
	body := `{"content":` + jsonQuote(newContent) + `}`
	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), owner), owner, org)
	rec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	getReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), owner), owner, org)
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

func TestUpdateAuthRestrictionsCanClearAllowlist(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	setBody := `{"content":"{\"domains\":[\"example.com\"]}"}`
	setReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(setBody)), owner), owner, org)
	setRec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(setRec, setReq)
	if setRec.Code != http.StatusOK {
		t.Fatalf("first update status = %d, want %d, body=%s", setRec.Code, http.StatusOK, setRec.Body.String())
	}

	emptyContent := `{"domains":[],"groups":[],"users":[]}`
	clearBody := `{"content":` + jsonQuote(emptyContent) + `}`
	clearReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(clearBody)), owner), owner, org)
	clearRec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear update status = %d, want %d, body=%s", clearRec.Code, http.StatusOK, clearRec.Body.String())
	}

	getReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), owner), owner, org)
	getRec := httptest.NewRecorder()
	h.GetAuthRestrictions(getRec, getReq)
	var getResp struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if getResp.Content != emptyContent {
		t.Errorf("content = %q, want %q after clearing", getResp.Content, emptyContent)
	}
}
