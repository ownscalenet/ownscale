package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		Domains   []string `json:"domains"`
		Groups    []string `json:"groups"`
		Users     []string `json:"users"`
		CanManage bool     `json:"canManage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Domains) != 0 || len(resp.Groups) != 0 || len(resp.Users) != 0 {
		t.Errorf("domains=%v groups=%v users=%v, want all empty", resp.Domains, resp.Groups, resp.Users)
	}
	if resp.CanManage {
		t.Error("canManage = true for a plain member, want false")
	}
}

func TestUpdateAuthRestrictionsRequiresManager(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	body := `{"domains":["example.com"]}`
	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), member), member, org)
	rec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateAuthRestrictionsPersistsAndReturnsOK(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	body := `{"domains":["Example.com","example.com","  ","other.com"],"groups":["group:eng"],"users":["alice@example.com"]}`
	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), owner), owner, org)
	rec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var putResp struct {
		Domains []string `json:"domains"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &putResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// "Example.com" and "example.com" are kept distinct (case-sensitive
	// dedup only, mirroring headplane's plain Set-based dedup); blanks are
	// dropped.
	if len(putResp.Domains) != 3 {
		t.Errorf("domains = %v, want 3 entries (blank dropped)", putResp.Domains)
	}

	getReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), owner), owner, org)
	getRec := httptest.NewRecorder()
	h.GetAuthRestrictions(getRec, getReq)

	var getResp struct {
		Domains []string `json:"domains"`
		Groups  []string `json:"groups"`
		Users   []string `json:"users"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(getResp.Domains) != 3 {
		t.Errorf("domains = %v, want 3 entries", getResp.Domains)
	}
	if len(getResp.Groups) != 1 || getResp.Groups[0] != "group:eng" {
		t.Errorf("groups = %v, want [group:eng]", getResp.Groups)
	}
	if len(getResp.Users) != 1 || getResp.Users[0] != "alice@example.com" {
		t.Errorf("users = %v, want [alice@example.com]", getResp.Users)
	}
}

func TestUpdateAuthRestrictionsCanClearAllowlist(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	setReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(`{"domains":["example.com"]}`)), owner), owner, org)
	setRec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(setRec, setReq)
	if setRec.Code != http.StatusOK {
		t.Fatalf("first update status = %d, want %d, body=%s", setRec.Code, http.StatusOK, setRec.Body.String())
	}

	clearReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(`{}`)), owner), owner, org)
	clearRec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear update status = %d, want %d, body=%s", clearRec.Code, http.StatusOK, clearRec.Body.String())
	}

	getReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), owner), owner, org)
	getRec := httptest.NewRecorder()
	h.GetAuthRestrictions(getRec, getReq)
	var getResp struct {
		Domains []string `json:"domains"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(getResp.Domains) != 0 {
		t.Errorf("domains = %v, want empty after clearing", getResp.Domains)
	}
}
