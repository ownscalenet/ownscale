package hscl

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/hscl/hstest"
)

// policy owns the tags the tests use, which headscale requires before a tag
// can be set on a node or key.
const policy = `{
	"tagOwners": {"tag:server": ["alice@"]},
	"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],
}`

// newTestClient returns a Client for a fresh real headscale holding policy and
// user alice, and the headscale's socket.
func newTestClient(t *testing.T) (*Client, string) {
	t.Helper()
	sock := hstest.Headscale(t)
	c := NewUnix(sock)
	if _, err := c.CreateUser(t.Context(), CreateUserParams{Name: "alice", Email: "alice@example.com"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := c.SetPolicy(t.Context(), policy); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	return c, sock
}

// registerNode registers a machine named name to user.
func registerNode(t *testing.T, c *Client, sock, user, name string, routes ...string) Node {
	t.Helper()
	node, err := c.RegisterNode(t.Context(), user, hstest.PendingNode(t, sock, user, name, routes...))
	if err != nil {
		t.Fatalf("RegisterNode(%q): %v", name, err)
	}
	return node
}

func userByName(t *testing.T, c *Client, name string) (User, bool) {
	t.Helper()
	users, err := c.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	for _, u := range users {
		if u.Name == name {
			return u, true
		}
	}
	return User{}, false
}

func nodeByID(t *testing.T, c *Client, id string) (Node, bool) {
	t.Helper()
	nodes, err := c.ListNodes(t.Context())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

func TestUsers(t *testing.T) {
	c, _ := newTestClient(t)

	created, err := c.CreateUser(t.Context(), CreateUserParams{
		Name:        "bob",
		Email:       "bob@example.com",
		DisplayName: "Bob",
		PictureURL:  "https://example.com/bob.png",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.ID == "" || created.Name != "bob" || created.Email != "bob@example.com" ||
		created.DisplayName != "Bob" || created.CreatedAt == "" {
		t.Errorf("CreateUser = %+v", created)
	}
	if got, ok := userByName(t, c, "bob"); !ok || got != created {
		t.Errorf("listed user = %+v, %v, want %+v", got, ok, created)
	}

	if err := c.RenameUser(t.Context(), created.ID, "robert"); err != nil {
		t.Fatalf("RenameUser: %v", err)
	}
	if _, ok := userByName(t, c, "robert"); !ok {
		t.Error("renamed user not listed")
	}

	if err := c.DeleteUser(t.Context(), created.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, ok := userByName(t, c, "robert"); ok {
		t.Error("deleted user still listed")
	}
}

func TestNodes(t *testing.T) {
	c, sock := newTestClient(t)
	alice, _ := userByName(t, c, "alice")

	node := registerNode(t, c, sock, "alice", "laptop", "10.0.0.0/24")
	if node.ID == "" || node.Name != "laptop" || node.GivenName != "laptop" || len(node.IPAddresses) != 2 ||
		node.RegisterMethod != "REGISTER_METHOD_CLI" || node.CreatedAt == "" {
		t.Errorf("RegisterNode = %+v", node)
	}
	if node.User == nil || node.User.ID != alice.ID {
		t.Errorf("node user = %+v, want alice", node.User)
	}

	if err := c.RenameNode(t.Context(), node.ID, "desktop"); err != nil {
		t.Fatalf("RenameNode: %v", err)
	}
	if err := c.SetApprovedRoutes(t.Context(), node.ID, []string{"10.0.0.0/24"}); err != nil {
		t.Fatalf("SetApprovedRoutes: %v", err)
	}
	got, _ := nodeByID(t, c, node.ID)
	if got.GivenName != "desktop" {
		t.Errorf("given name = %q, want desktop", got.GivenName)
	}
	if !slices.Equal(got.ApprovedRoutes, []string{"10.0.0.0/24"}) {
		t.Errorf("approved routes = %v, want [10.0.0.0/24]", got.ApprovedRoutes)
	}

	if err := c.ExpireNode(t.Context(), node.ID); err != nil {
		t.Fatalf("ExpireNode: %v", err)
	}
	got, _ = nodeByID(t, c, node.ID)
	if got.Expiry == nil {
		t.Fatal("expiry = nil after ExpireNode")
	}
	if expiry, err := time.Parse(time.RFC3339, *got.Expiry); err != nil || expiry.After(time.Now()) {
		t.Errorf("expiry = %q, want a past RFC3339 time", *got.Expiry)
	}

	// Tagging a node hands its ownership from the user to the tag; headscale
	// lists tagged nodes under a placeholder user.
	if err := c.SetTags(t.Context(), node.ID, []string{"tag:server"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}
	got, _ = nodeByID(t, c, node.ID)
	if !slices.Equal(got.Tags, []string{"tag:server"}) || got.User != nil && got.User.ID == alice.ID {
		t.Errorf("tagged node tags = %v, user = %+v, want [tag:server] and not owned by alice", got.Tags, got.User)
	}

	if err := c.DeleteNode(t.Context(), node.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if _, ok := nodeByID(t, c, node.ID); ok {
		t.Error("deleted node still listed")
	}
}

func TestPreAuthKeys(t *testing.T) {
	c, _ := newTestClient(t)
	alice, _ := userByName(t, c, "alice")
	expiration := time.Now().Add(24 * time.Hour).Truncate(time.Second)

	tests := []struct {
		name     string
		params   CreatePreAuthKeyParams
		wantUser string
	}{
		{"user key", CreatePreAuthKeyParams{UserID: alice.ID, Reusable: true, Expiration: expiration}, alice.ID},
		{"tag-only key", CreatePreAuthKeyParams{Ephemeral: true, Expiration: expiration, ACLTags: []string{"tag:server"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := c.CreatePreAuthKey(t.Context(), tt.params)
			if err != nil {
				t.Fatalf("CreatePreAuthKey: %v", err)
			}
			if key.ID == "" || key.Key == "" || key.Reusable != tt.params.Reusable || key.Ephemeral != tt.params.Ephemeral ||
				!slices.Equal(key.ACLTags, tt.params.ACLTags) && len(tt.params.ACLTags) > 0 {
				t.Errorf("CreatePreAuthKey = %+v", key)
			}
			if gotUser := key.User; (gotUser == nil) != (tt.wantUser == "") || gotUser != nil && gotUser.ID != tt.wantUser {
				t.Errorf("key user = %+v, want %q", gotUser, tt.wantUser)
			}
			if got, err := time.Parse(time.RFC3339, key.Expiration); err != nil || !got.Equal(expiration) {
				t.Errorf("expiration = %q, want %v", key.Expiration, expiration)
			}

			listed := func() (PreAuthKey, bool) {
				keys, err := c.ListPreAuthKeys(t.Context())
				if err != nil {
					t.Fatalf("ListPreAuthKeys: %v", err)
				}
				i := slices.IndexFunc(keys, func(k PreAuthKey) bool { return k.ID == key.ID })
				if i < 0 {
					return PreAuthKey{}, false
				}
				return keys[i], true
			}
			if _, ok := listed(); !ok {
				t.Fatal("created key not listed")
			}

			if err := c.ExpirePreAuthKey(t.Context(), key.ID); err != nil {
				t.Fatalf("ExpirePreAuthKey: %v", err)
			}
			expired, _ := listed()
			if got, err := time.Parse(time.RFC3339, expired.Expiration); err != nil || got.After(time.Now()) {
				t.Errorf("expiration after ExpirePreAuthKey = %q, want a past time", expired.Expiration)
			}

			if err := c.DeletePreAuthKey(t.Context(), key.ID); err != nil {
				t.Fatalf("DeletePreAuthKey: %v", err)
			}
			if _, ok := listed(); ok {
				t.Error("deleted key still listed")
			}
		})
	}
}

func TestPolicy(t *testing.T) {
	c, _ := newTestClient(t)

	got, err := c.GetPolicy(t.Context())
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if got != policy {
		t.Errorf("GetPolicy = %q, want the policy set", got)
	}

	// headscale's application/problem+json reason reaches the caller.
	err = c.SetPolicy(t.Context(), `{"acls": [{"action": "bogus", "src": ["*"], "dst": ["*:*"]}]}`)
	if err == nil {
		t.Fatal("SetPolicy with an invalid action: want error, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("SetPolicy error = %q, want it to name the invalid action", err)
	}
}

func TestDeleteUserAndNodes(t *testing.T) {
	tests := []struct {
		name      string
		user      string
		wantUsers []string
		wantNodes []string
	}{
		{"user with nodes", "alice", []string{"bob"}, []string{"bob-laptop", "server"}},
		{"already deleted user", "nobody", []string{"alice", "bob"}, []string{"alice-laptop", "alice-phone", "bob-laptop", "server"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, sock := newTestClient(t)
			if _, err := c.CreateUser(t.Context(), CreateUserParams{Name: "bob"}); err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			registerNode(t, c, sock, "alice", "alice-laptop")
			registerNode(t, c, sock, "alice", "alice-phone")
			registerNode(t, c, sock, "bob", "bob-laptop")
			server := registerNode(t, c, sock, "alice", "server")
			if err := c.SetTags(t.Context(), server.ID, []string{"tag:server"}); err != nil {
				t.Fatalf("SetTags: %v", err)
			}

			id := "999"
			if u, ok := userByName(t, c, tt.user); ok {
				id = u.ID
			}
			if err := c.DeleteUserAndNodes(t.Context(), id); err != nil {
				t.Fatalf("DeleteUserAndNodes: %v", err)
			}

			users, err := c.ListUsers(t.Context())
			if err != nil {
				t.Fatalf("ListUsers: %v", err)
			}
			var gotUsers []string
			for _, u := range users {
				gotUsers = append(gotUsers, u.Name)
			}
			nodes, err := c.ListNodes(t.Context())
			if err != nil {
				t.Fatalf("ListNodes: %v", err)
			}
			var gotNodes []string
			for _, n := range nodes {
				gotNodes = append(gotNodes, n.Name)
			}
			slices.Sort(gotUsers)
			slices.Sort(gotNodes)
			if !slices.Equal(gotUsers, tt.wantUsers) {
				t.Errorf("users = %v, want %v", gotUsers, tt.wantUsers)
			}
			if !slices.Equal(gotNodes, tt.wantNodes) {
				t.Errorf("nodes = %v, want %v", gotNodes, tt.wantNodes)
			}
		})
	}
}

// TestNonJSONResponseIsAnError checks that a 200 without a JSON body, e.g.
// from something other than headscale answering on the socket, fails
// instead of being used as an empty result.
func TestNonJSONResponseIsAnError(t *testing.T) {
	c := NewUnix(hstest.Socket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "hello")
	})))

	if _, err := c.ListNodes(t.Context()); err == nil {
		t.Error("ListNodes: want error, got nil")
	}
}
