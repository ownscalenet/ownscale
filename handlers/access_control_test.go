package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/hscl/hstest"
)

// newFakePolicyServer builds a fake implementing headscale's
// policy endpoint (GET/PUT /api/v1/policy) backed by an in-memory policy
// string, and a *hscl.Client pointed at it.
func newFakePolicyServer(t *testing.T, initial string) (*hscl.Client, *string) {
	t.Helper()
	policy := initial

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/policy", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			fmt.Fprintf(w, `{"policy":%s}`, jsonQuote(policy))
		case http.MethodPut:
			var body struct {
				Policy string `json:"policy"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			policy = body.Policy
			fmt.Fprintf(w, `{"policy":%s}`, jsonQuote(policy))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	return hscl.NewUnix(hstest.Socket(t, mux)), &policy
}

func TestGetAccessControlPolicyReturnsEmptyStringAsIs(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleMember)
	member := users[0]
	// orgs.Service seeds a default policy when it starts an organization's
	// headscale; this test simulates
	// the (otherwise-unexpected) case where headscale genuinely reports no
	// policy configured, and asserts the handler passes it through as-is
	// with no fallback/special-casing.
	client, _ := newFakePolicyServer(t, "")

	req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodGet, "/api/access-control", nil), member), client)
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
	if resp.Content != "" {
		t.Errorf("content = %q, want empty string (no fallback)", resp.Content)
	}
	if resp.CanManage {
		t.Error("canManage = true for a plain member, want false")
	}
}

func TestUpdateAccessControlPolicyRequiresManager(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleMember)
	member := users[0]
	client, _ := newFakePolicyServer(t, "")

	body := `{"content":"{\"acls\":[]}"}`
	req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPut, "/api/access-control", strings.NewReader(body)), member), client)
	rec := httptest.NewRecorder()
	h.UpdateAccessControlPolicy(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateAccessControlPolicyRejectsInvalidHuJSON(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]
	client, _ := newFakePolicyServer(t, "")

	body := `{"content":"not valid hujson {{{"}`
	req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPut, "/api/access-control", strings.NewReader(body)), owner), client)
	rec := httptest.NewRecorder()
	h.UpdateAccessControlPolicy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdateAccessControlPolicyPersistsAndReturnsOK(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]
	client, stored := newFakePolicyServer(t, "")

	newPolicy := `{"acls":[{"action":"accept","src":["tag:test"],"dst":["*:*"]}]}`
	body := `{"content":` + jsonQuote(newPolicy) + `}`
	req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPut, "/api/access-control", strings.NewReader(body)), owner), client)
	rec := httptest.NewRecorder()
	h.UpdateAccessControlPolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if *stored != newPolicy {
		t.Fatalf("policy stored on the fake headscale server = %q, want %q", *stored, newPolicy)
	}

	getReq := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodGet, "/api/access-control", nil), owner), client)
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
