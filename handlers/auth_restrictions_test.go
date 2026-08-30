package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// TestGetAuthRestrictionsPendingOrgIsNotConfigured covers a pending (not
// yet provisioned) organization: no allowlist to show yet.
func TestGetAuthRestrictionsPendingOrgIsNotConfigured(t *testing.T) {
	h, queries := newTestHandlers(t, true)

	org, err := h.orgs.CreatePending(t.Context(), "acme")
	if err != nil {
		t.Fatalf("orgs.CreatePending: %v", err)
	}
	user := insertUser(t, queries)
	if err := queries.SetUserOrganizationAndRole(t.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
		OrganizationID: sql.NullString{String: org.ID, Valid: true},
		Role:           RoleOwner,
		ID:             user.ID,
	}); err != nil {
		t.Fatalf("SetUserOrganizationAndRole: %v", err)
	}
	user, err = queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), user)
	rec := httptest.NewRecorder()
	h.GetAuthRestrictions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		NotConfigured bool `json:"notConfigured"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.NotConfigured {
		t.Error("notConfigured = false for a pending organization, want true")
	}
}

func TestGetAuthRestrictionsDefaultsToEmpty(t *testing.T) {
	h, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), member)
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
	h, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	body := `{"domains":["example.com"]}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), member)
	rec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateAuthRestrictionsPersistsAndReturnsOK(t *testing.T) {
	h, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	body := `{"domains":["Example.com","example.com","  ","other.com"],"groups":["group:eng"],"users":["alice@example.com"]}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(body)), owner)
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

	getReq := withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), owner)
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
	h, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	setReq := withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(`{"domains":["example.com"]}`)), owner)
	setRec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(setRec, setReq)
	if setRec.Code != http.StatusOK {
		t.Fatalf("first update status = %d, want %d, body=%s", setRec.Code, http.StatusOK, setRec.Body.String())
	}

	clearReq := withTestUser(httptest.NewRequest(http.MethodPut, "/api/auth-restrictions", strings.NewReader(`{}`)), owner)
	clearRec := httptest.NewRecorder()
	h.UpdateAuthRestrictions(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear update status = %d, want %d, body=%s", clearRec.Code, http.StatusOK, clearRec.Body.String())
	}

	getReq := withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), owner)
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
