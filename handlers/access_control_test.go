package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/dockerctl"
)

// newAccessControlTestHandlers builds Handlers with organization
// provisioning configured (dockerfake-backed), synchronously creates and
// provisions an active organization, and inserts one user per role in
// roles (in order).
func newAccessControlTestHandlers(t *testing.T, roles ...string) (*Handlers, []sqlcgen.User) {
	t.Helper()
	h, queries := newTestHandlers(t, true)

	org, err := h.orgs.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("orgs.Create: %v", err)
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
	return h, users
}

// TestGetAccessControlPolicyPendingOrgIsNotConfigured covers a pending
// (not yet provisioned) organization: no ACL policy to show yet.
func TestGetAccessControlPolicyPendingOrgIsNotConfigured(t *testing.T) {
	h, queries := newTestHandlers(t, true)

	// CreatePending, unlike Create, leaves the org "pending".
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

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/access-control", nil), user)
	rec := httptest.NewRecorder()
	h.GetAccessControlPolicy(rec, req)

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

func TestGetAccessControlPolicyDefaultsToDockerctlDefault(t *testing.T) {
	h, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/access-control", nil), member)
	rec := httptest.NewRecorder()
	h.GetAccessControlPolicy(rec, req)

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
	if resp.Content != dockerctl.DefaultACLPolicy {
		t.Errorf("content = %q, want default policy", resp.Content)
	}
	if resp.CanManage {
		t.Error("canManage = true for a plain member, want false")
	}
}

func TestUpdateAccessControlPolicyRequiresManager(t *testing.T) {
	h, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	body := `{"content":"{\"acls\":[]}"}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/access-control", strings.NewReader(body)), member)
	rec := httptest.NewRecorder()
	h.UpdateAccessControlPolicy(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateAccessControlPolicyRejectsInvalidHuJSON(t *testing.T) {
	h, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	body := `{"content":"not valid hujson {{{"}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/access-control", strings.NewReader(body)), owner)
	rec := httptest.NewRecorder()
	h.UpdateAccessControlPolicy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdateAccessControlPolicyPersistsAndReturnsOK(t *testing.T) {
	h, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	newPolicy := `{"acls":[{"action":"accept","src":["tag:test"],"dst":["*:*"]}]}`
	body := `{"content":` + jsonQuote(newPolicy) + `}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/access-control", strings.NewReader(body)), owner)
	rec := httptest.NewRecorder()
	h.UpdateAccessControlPolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	getReq := withTestUser(httptest.NewRequest(http.MethodGet, "/api/access-control", nil), owner)
	getRec := httptest.NewRecorder()
	h.GetAccessControlPolicy(getRec, getReq)

	var resp struct {
		Content   string `json:"content"`
		CanManage bool   `json:"canManage"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Content != newPolicy {
		t.Errorf("content = %q, want %q", resp.Content, newPolicy)
	}
	if !resp.CanManage {
		t.Error("canManage = false for an owner, want true")
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestFormatAccessControlPolicyPrettyPrints(t *testing.T) {
	h, _ := newTestHandlers(t, false)

	body := `{"content":"{\"acls\":[{\"action\":\"accept\",\"src\":[\"*\"],\"dst\":[\"*:*\"]}]}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/access-control/format", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.FormatAccessControlPolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Content == "" || !strings.Contains(resp.Content, "\n") {
		t.Errorf("content = %q, want multi-line pretty-printed HuJSON", resp.Content)
	}
}

func TestFormatAccessControlPolicyRejectsInvalidHuJSON(t *testing.T) {
	h, _ := newTestHandlers(t, false)

	body := `{"content":"not valid hujson {{{"}`
	req := httptest.NewRequest(http.MethodPost, "/api/access-control/format", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.FormatAccessControlPolicy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
