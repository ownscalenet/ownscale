package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/headscaleclient"
)

func TestListAuthKeysReturnsKeysAndUsers(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, _ := activeOrgUser(t, h, queries)

	req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-keys", nil), user), client)
	rec := httptest.NewRecorder()
	h.ListAuthKeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AuthKeys []headscaleclient.PreAuthKey `json:"authKeys"`
		Users    []headscaleclient.User       `json:"users"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.AuthKeys) != 1 || body.AuthKeys[0].ID != "1" {
		t.Errorf("unexpected auth keys: %+v", body.AuthKeys)
	}
	if len(body.Users) != 2 {
		t.Errorf("got %d users, want 2", len(body.Users))
	}
}

// TestCreateAuthKeyValidation covers the two request-body validation
// errors CreateAuthKey rejects with 400 before ever calling the fake
// headscale server.
func TestCreateAuthKeyValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "requires user or tags", body: `{"expiryDays":90}`},
		{name: "requires expiry days", body: `{"userId":"1","expiryDays":0}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, queries := newMachinesTestHandlers(t)
			user, client, _ := activeOrgUser(t, h, queries)

			req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys", strings.NewReader(tc.body)), user), client)
			rec := httptest.NewRecorder()
			h.CreateAuthKey(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCreateAuthKeySuccess(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, requests := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"userId":"1","reusable":true,"expiryDays":90,"aclTags":["tag:server"]}`)
	req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys", body), user), client)
	rec := httptest.NewRecorder()
	h.CreateAuthKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AuthKey headscaleclient.PreAuthKey `json:"authKey"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AuthKey.Key != "nodekey:newkey" {
		t.Errorf("unexpected auth key: %+v", resp.AuthKey)
	}
	last := (*requests)[len(*requests)-1]
	if last.Method != http.MethodPost || !strings.HasSuffix(last.URL.Path, "/preauthkey") {
		t.Errorf("unexpected upstream request: %s %s", last.Method, last.URL.Path)
	}
}

func TestCreateAuthKeyTagOnly(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, _ := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"aclTags":["tag:server"],"expiryDays":90}`)
	req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys", body), user), client)
	rec := httptest.NewRecorder()
	h.CreateAuthKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestExpireAuthKey(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, requests := activeOrgUser(t, h, queries)

	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys/1/expire", nil), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.ExpireAuthKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if !strings.HasSuffix(last.URL.Path, "/preauthkey/expire") {
		t.Errorf("unexpected upstream request path: %s", last.URL.Path)
	}
}
