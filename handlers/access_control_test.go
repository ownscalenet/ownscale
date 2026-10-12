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

// failingPolicy makes the fake headscale reject a policy, as it does for a
// failing "tests" entry.
const failingPolicy = `{"tests":[{"src":"alice@","deny":["10.0.0.2:22"]}]}`

// rejectPolicy writes headscale's problem+json for a policy whose tests fail.
func rejectPolicy(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprint(w, `{"title":"Bad Request","status":400,"detail":"setting policy","errors":[{"message":"test(s) failed:\nalice@ -> 10.0.0.2:22: expected DENIED, got ALLOWED"}]}`)
}

// newFakePolicyServer builds a fake implementing headscale's
// policy endpoints (GET/PUT /api/v1/policy and POST /api/v1/policy/check)
// backed by an in-memory policy string, and a *hscl.Client pointed at it.
// It rejects failingPolicy.
func newFakePolicyServer(t *testing.T, initial string) (*hscl.Client, *string) {
	t.Helper()
	policy := initial

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/policy/check", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Policy string `json:"policy"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Policy == failingPolicy {
			rejectPolicy(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
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
			if body.Policy == failingPolicy {
				rejectPolicy(w)
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

func TestAccessControlPolicyTests(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		content    string
		check      bool
		wantStatus int
		wantError  string
	}{
		{"check passes", RoleOwner, `{"acls":[]}`, true, http.StatusOK, ""},
		{"check shows the failing test", RoleOwner, failingPolicy, true, http.StatusBadRequest, "alice@ -> 10.0.0.2:22: expected DENIED"},
		{"save shows the failing test", RoleOwner, failingPolicy, false, http.StatusBadRequest, "alice@ -> 10.0.0.2:22: expected DENIED"},
		{"check rejects invalid HuJSON", RoleOwner, `{{{`, true, http.StatusBadRequest, "invalid HuJSON"},
		{"check needs a manager", RoleMember, `{"acls":[]}`, true, http.StatusForbidden, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, users := newOrgTestHandlers(t, tt.role)
			client, stored := newFakePolicyServer(t, "")
			handle, method, path := h.UpdateAccessControlPolicy, http.MethodPut, "/api/access-control"
			if tt.check {
				handle, method, path = h.CheckAccessControlPolicy, http.MethodPost, path+"/check"
			}

			body := `{"content":` + jsonQuote(tt.content) + `}`
			req := withHeadscaleClient(withTestUser(httptest.NewRequest(method, path, strings.NewReader(body)), users[0]), client)
			rec := httptest.NewRecorder()
			handle(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			var resp struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if !strings.Contains(resp.Error, tt.wantError) {
				t.Errorf("error = %q, want it to contain %q", resp.Error, tt.wantError)
			}
			if tt.check && *stored != "" {
				t.Errorf("check saved the policy: %q", *stored)
			}
		})
	}
}
