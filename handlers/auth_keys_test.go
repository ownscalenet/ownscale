package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/headscaleclient"
)

func TestListAuthKeysNotConfiguredNoRegistry(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := insertUser(t, queries)

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-keys", nil), user)
	rec := httptest.NewRecorder()
	h.ListAuthKeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		NotConfigured bool `json:"notConfigured"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.NotConfigured {
		t.Error("expected notConfigured=true")
	}
}

func TestListAuthKeysReturnsKeysAndUsers(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _ := activeOrgUser(t, h, queries)

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-keys", nil), user)
	rec := httptest.NewRecorder()
	h.ListAuthKeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		NotConfigured bool                         `json:"notConfigured"`
		AuthKeys      []headscaleclient.PreAuthKey `json:"authKeys"`
		Users         []headscaleclient.User       `json:"users"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.NotConfigured {
		t.Fatal("expected notConfigured=false for an active organization")
	}
	if len(body.AuthKeys) != 1 || body.AuthKeys[0].ID != "1" {
		t.Errorf("unexpected auth keys: %+v", body.AuthKeys)
	}
	if len(body.Users) != 2 {
		t.Errorf("got %d users, want 2", len(body.Users))
	}
}

func TestCreateAuthKeyRequiresUserOrTags(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _ := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"expiryDays":90}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys", body), user)
	rec := httptest.NewRecorder()
	h.CreateAuthKey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAuthKeyRequiresExpiryDays(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _ := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"userId":"1","expiryDays":0}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys", body), user)
	rec := httptest.NewRecorder()
	h.CreateAuthKey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAuthKeySuccess(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, requests := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"userId":"1","reusable":true,"expiryDays":90,"aclTags":["tag:server"]}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys", body), user)
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
	user, _ := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"aclTags":["tag:server"],"expiryDays":90}`)
	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys", body), user)
	rec := httptest.NewRecorder()
	h.CreateAuthKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestExpireAuthKey(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, requests := activeOrgUser(t, h, queries)

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys/1/expire", nil), user), "id", "1")
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

func TestExpireAuthKeyMissingID(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _ := activeOrgUser(t, h, queries)

	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/auth-keys//expire", nil), user)
	rec := httptest.NewRecorder()
	h.ExpireAuthKey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
