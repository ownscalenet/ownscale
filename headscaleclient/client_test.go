package headscaleclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer builds an httptest.Server implementing the handful of
// headscale management endpoints headscaleclient.Client uses, recording
// requests for assertions and requiring the given bearer token on every
// call (mirroring headscale's real behavior). Request bodies are drained
// and replaced with a fresh reader before recording, so tests can decode
// them after the handler (and the server) is done with the request.
func newTestServer(t *testing.T, wantToken string) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var requests []*http.Request
	record := func(r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		requests = append(requests, r)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"nodes":[{
			"id":"1",
			"machineKey":"mkey:abc",
			"nodeKey":"nkey:def",
			"ipAddresses":["100.64.0.1"],
			"name":"node1",
			"givenName":"node1",
			"user":{"id":"1","name":"alice","displayName":"Alice","email":"alice@example.com","createdAt":"2024-01-01T00:00:00Z"},
			"lastSeen":"2024-02-01T00:00:00Z",
			"expiry":"0001-01-01T00:00:00Z",
			"createdAt":"2024-01-01T00:00:00Z",
			"registerMethod":"REGISTER_METHOD_OIDC",
			"online":true,
			"tags":["tag:server"],
			"approvedRoutes":["10.0.0.0/24"],
			"availableRoutes":["10.0.0.0/24","192.168.0.0/24"],
			"subnetRoutes":["10.0.0.0/24"]
		}]}`)
	})
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			body := CreateUserParams{}
			_ = json.Unmarshal(raw, &body)
			resp := map[string]any{"user": map[string]any{
				"id":          "3",
				"name":        body.Name,
				"displayName": body.DisplayName,
				"email":       body.Email,
				"pictureUrl":  body.PictureURL,
				"createdAt":   "2024-01-01T00:00:00Z",
			}}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		fmt.Fprint(w, `{"users":[{"id":"1","name":"alice","displayName":"Alice","email":"alice@example.com","createdAt":"2024-01-01T00:00:00Z"}]}`)
	})
	mux.HandleFunc("/api/v1/user/1/rename/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"user":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/user/1", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/node/1/rename/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1/tags", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1/approve_routes", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1/expire", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/preauthkey", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"preAuthKey":{
				"id":"2",
				"key":"nodekey:abc123",
				"user":{"id":"1","name":"alice","displayName":"Alice","email":"alice@example.com","createdAt":"2024-01-01T00:00:00Z"},
				"reusable":true,
				"ephemeral":false,
				"used":false,
				"aclTags":["tag:server"],
				"createdAt":"2024-03-01T00:00:00Z",
				"expiration":"2024-06-01T00:00:00Z"
			}}`)
			return
		}
		fmt.Fprint(w, `{"preAuthKeys":[{
			"id":"1",
			"key":"nodekey:def456",
			"user":{"id":"1","name":"alice","displayName":"Alice","email":"alice@example.com","createdAt":"2024-01-01T00:00:00Z"},
			"reusable":false,
			"ephemeral":false,
			"used":false,
			"aclTags":[],
			"createdAt":"2024-01-01T00:00:00Z",
			"expiration":"2024-06-01T00:00:00Z"
		}]}`)
	})
	mux.HandleFunc("/api/v1/preauthkey/expire", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})

	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})

	srv := httptest.NewServer(authed)
	return srv, &requests
}

func newTestClient(t *testing.T) (*Client, *httptest.Server, *[]*http.Request) {
	t.Helper()
	srv, reqs := newTestServer(t, "test-key")
	host := strings.TrimPrefix(srv.URL, "http://")
	c := New(host, "test-key")
	return c, srv, reqs
}

func TestClientListNodes(t *testing.T) {
	c, srv, _ := newTestClient(t)
	defer srv.Close()

	nodes, err := c.ListNodes(t.Context())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	n := nodes[0]
	if n.ID != "1" || n.Name != "node1" || !n.Online {
		t.Errorf("unexpected node: %+v", n)
	}
	if len(n.Tags) != 1 || n.Tags[0] != "tag:server" {
		t.Errorf("unexpected tags: %v", n.Tags)
	}
	if n.User == nil || n.User.Name != "alice" {
		t.Errorf("unexpected user: %+v", n.User)
	}
	if n.LastSeen == nil || *n.LastSeen == "" {
		t.Errorf("expected non-nil LastSeen")
	}
	if n.Expiry != nil {
		t.Errorf("expected nil Expiry for zero-value expiry, got %v", *n.Expiry)
	}
	if n.RegisterMethod != "REGISTER_METHOD_OIDC" {
		t.Errorf("unexpected register method: %q", n.RegisterMethod)
	}
}

func TestClientListUsers(t *testing.T) {
	c, srv, _ := newTestClient(t)
	defer srv.Close()

	users, err := c.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 || users[0].Name != "alice" {
		t.Errorf("unexpected users: %+v", users)
	}
}

func TestClientRenameNode(t *testing.T) {
	c, srv, reqs := newTestClient(t)
	defer srv.Close()

	if err := c.RenameNode(t.Context(), "1", "new-name"); err != nil {
		t.Fatalf("RenameNode: %v", err)
	}
	last := (*reqs)[len(*reqs)-1]
	if !strings.HasSuffix(last.URL.Path, "/rename/new-name") {
		t.Errorf("unexpected request path: %s", last.URL.Path)
	}
}

func TestClientCreateUser(t *testing.T) {
	c, srv, reqs := newTestClient(t)
	defer srv.Close()

	user, err := c.CreateUser(t.Context(), CreateUserParams{
		Name:        "created-user",
		Email:       "created-user@example.com",
		DisplayName: "Created User",
		PictureURL:  "https://example.com/avatar.png",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.Name != "created-user" || user.Email != "created-user@example.com" || user.DisplayName != "Created User" {
		t.Errorf("unexpected user: %+v", user)
	}
	last := (*reqs)[len(*reqs)-1]
	if last.Method != http.MethodPost || last.URL.Path != "/api/v1/user" {
		t.Errorf("unexpected request: %s %s", last.Method, last.URL.Path)
	}
	body := CreateUserParams{}
	if err := json.NewDecoder(last.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Name != "created-user" || body.Email != "created-user@example.com" ||
		body.DisplayName != "Created User" || body.PictureURL != "https://example.com/avatar.png" {
		t.Errorf("unexpected request body: %+v", body)
	}
}

func TestClientRenameUser(t *testing.T) {
	c, srv, reqs := newTestClient(t)
	defer srv.Close()

	if err := c.RenameUser(t.Context(), "1", "new-name"); err != nil {
		t.Fatalf("RenameUser: %v", err)
	}
	last := (*reqs)[len(*reqs)-1]
	if !strings.HasSuffix(last.URL.Path, "/rename/new-name") {
		t.Errorf("unexpected request path: %s", last.URL.Path)
	}
}

func TestClientDeleteUser(t *testing.T) {
	c, srv, reqs := newTestClient(t)
	defer srv.Close()

	if err := c.DeleteUser(t.Context(), "1"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	last := (*reqs)[len(*reqs)-1]
	if last.Method != http.MethodDelete || last.URL.Path != "/api/v1/user/1" {
		t.Errorf("unexpected request: %s %s", last.Method, last.URL.Path)
	}
}

func TestClientSetTags(t *testing.T) {
	c, srv, reqs := newTestClient(t)
	defer srv.Close()

	if err := c.SetTags(t.Context(), "1", []string{"tag:a", "tag:b"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}
	last := (*reqs)[len(*reqs)-1]
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(last.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Tags) != 2 {
		t.Errorf("unexpected tags sent: %v", body.Tags)
	}
}

func TestClientSetApprovedRoutes(t *testing.T) {
	c, srv, reqs := newTestClient(t)
	defer srv.Close()

	if err := c.SetApprovedRoutes(t.Context(), "1", []string{"10.0.0.0/24"}); err != nil {
		t.Fatalf("SetApprovedRoutes: %v", err)
	}
	last := (*reqs)[len(*reqs)-1]
	if !strings.HasSuffix(last.URL.Path, "/approve_routes") {
		t.Errorf("unexpected request path: %s", last.URL.Path)
	}
}

func TestClientExpireNode(t *testing.T) {
	c, srv, _ := newTestClient(t)
	defer srv.Close()

	if err := c.ExpireNode(t.Context(), "1"); err != nil {
		t.Fatalf("ExpireNode: %v", err)
	}
}

func TestClientDeleteNode(t *testing.T) {
	c, srv, reqs := newTestClient(t)
	defer srv.Close()

	if err := c.DeleteNode(t.Context(), "1"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	last := (*reqs)[len(*reqs)-1]
	if last.Method != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", last.Method)
	}
}

func TestClientListPreAuthKeys(t *testing.T) {
	c, srv, _ := newTestClient(t)
	defer srv.Close()

	keys, err := c.ListPreAuthKeys(t.Context())
	if err != nil {
		t.Fatalf("ListPreAuthKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	k := keys[0]
	if k.ID != "1" || k.Key != "nodekey:def456" {
		t.Errorf("unexpected key: %+v", k)
	}
	if k.User == nil || k.User.Name != "alice" {
		t.Errorf("unexpected user: %+v", k.User)
	}
	if k.Expiration == "" {
		t.Errorf("expected non-empty expiration")
	}
}

func TestClientCreatePreAuthKey(t *testing.T) {
	c, srv, reqs := newTestClient(t)
	defer srv.Close()

	key, err := c.CreatePreAuthKey(t.Context(), CreatePreAuthKeyParams{
		UserID:     "1",
		Reusable:   true,
		Expiration: time.Now().Add(90 * 24 * time.Hour),
		ACLTags:    []string{"tag:server"},
	})
	if err != nil {
		t.Fatalf("CreatePreAuthKey: %v", err)
	}
	if key.ID != "2" || key.Key != "nodekey:abc123" || !key.Reusable {
		t.Errorf("unexpected key: %+v", key)
	}
	last := (*reqs)[len(*reqs)-1]
	if last.Method != http.MethodPost || last.URL.Path != "/api/v1/preauthkey" {
		t.Errorf("unexpected request: %s %s", last.Method, last.URL.Path)
	}
	var body struct {
		User     string   `json:"user"`
		Reusable bool     `json:"reusable"`
		ACLTags  []string `json:"aclTags"`
	}
	if err := json.NewDecoder(last.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.User != "1" || !body.Reusable || len(body.ACLTags) != 1 {
		t.Errorf("unexpected request body: %+v", body)
	}
}

func TestClientExpirePreAuthKey(t *testing.T) {
	c, srv, reqs := newTestClient(t)
	defer srv.Close()

	if err := c.ExpirePreAuthKey(t.Context(), "1"); err != nil {
		t.Fatalf("ExpirePreAuthKey: %v", err)
	}
	last := (*reqs)[len(*reqs)-1]
	if last.Method != http.MethodPost || last.URL.Path != "/api/v1/preauthkey/expire" {
		t.Errorf("unexpected request: %s %s", last.Method, last.URL.Path)
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(last.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.ID != "1" {
		t.Errorf("unexpected request body: %+v", body)
	}
}

func TestClientAuthFailureSurfacesAsError(t *testing.T) {
	srv, _ := newTestServer(t, "correct-key")
	defer srv.Close()

	c := New(strings.TrimPrefix(srv.URL, "http://"), "wrong-key")
	_, err := c.ListNodes(t.Context())
	if err == nil {
		t.Fatal("expected an error for a bad API key, got nil")
	}
}

// fakeMinter is a ClientMinter test double recording which subdomains it
// was asked to build clients for.
type fakeMinter struct {
	minted map[string]int
	key    string
	err    error
}

func (m *fakeMinter) MintClient(_ context.Context, subdomain string) (*Client, error) {
	if m.minted == nil {
		m.minted = make(map[string]int)
	}
	m.minted[subdomain]++
	if m.err != nil {
		return nil, m.err
	}
	return New("hs-"+subdomain+":8080", m.key), nil
}

func TestRegistryMintsOnceAndCaches(t *testing.T) {
	minter := &fakeMinter{key: "minted-key"}
	r := NewRegistry(minter)

	c1, err := r.Client(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	c2, err := r.Client(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Client (second call): %v", err)
	}
	if c1 != c2 {
		t.Error("expected the same cached *Client on the second call")
	}
	if minter.minted["acme"] != 1 {
		t.Errorf("expected exactly 1 mint for acme, got %d", minter.minted["acme"])
	}
}

func TestRegistryPropagatesMintError(t *testing.T) {
	wantErr := errors.New("container not running")
	r := NewRegistry(&fakeMinter{err: wantErr})

	_, err := r.Client(t.Context(), "acme")
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("Client err = %v, want wrapping of %v", err, wantErr)
	}
}

func TestRegistryDifferentSubdomainsGetDifferentClients(t *testing.T) {
	minter := &fakeMinter{key: "minted-key"}
	r := NewRegistry(minter)

	c1, err := r.Client(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Client(acme): %v", err)
	}
	c2, err := r.Client(t.Context(), "widgets")
	if err != nil {
		t.Fatalf("Client(widgets): %v", err)
	}
	if c1 == c2 {
		t.Error("expected distinct clients for distinct subdomains")
	}
	if minter.minted["acme"] != 1 || minter.minted["widgets"] != 1 {
		t.Errorf("unexpected mint counts: %v", minter.minted)
	}
}
