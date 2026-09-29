package hstest

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync"
)

// Node is a node in a Users fake. UserID is empty for tagged nodes.
type Node struct {
	ID     string
	UserID string
}

// Users is a stateful fake of headscale's user and node endpoints. Like
// headscale, it refuses to delete a user that still owns nodes.
type Users struct {
	mu      sync.Mutex
	userIDs []string
	nodes   []Node
}

// NewUsers returns a Users fake holding userIDs and nodes.
func NewUsers(userIDs []string, nodes []Node) *Users {
	return &Users{userIDs: userIDs, nodes: nodes}
}

// UserIDs returns the IDs of the users that haven't been deleted.
func (u *Users) UserIDs() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.userIDs)
}

// NodeIDs returns the IDs of the nodes that haven't been deleted.
func (u *Users) NodeIDs() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	ids := make([]string, len(u.nodes))
	for i, n := range u.nodes {
		ids[i] = n.ID
	}
	return ids
}

// Handler serves the fake's endpoints.
func (u *Users) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		nodes := make([]map[string]any, len(u.nodes))
		for i, n := range u.nodes {
			nodes[i] = map[string]any{"id": n.ID}
			if n.UserID != "" {
				nodes[i]["user"] = map[string]any{"id": n.UserID}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
	})
	mux.HandleFunc("DELETE /api/v1/node/{id}", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.nodes = slices.DeleteFunc(u.nodes, func(n Node) bool { return n.ID == r.PathValue("id") })
		writeJSON(w, http.StatusOK, map[string]any{})
	})
	mux.HandleFunc("GET /api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		users := make([]map[string]any, len(u.userIDs))
		for i, id := range u.userIDs {
			users[i] = map[string]any{"id": id}
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
	})
	mux.HandleFunc("DELETE /api/v1/user/{id}", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		id := r.PathValue("id")
		if slices.ContainsFunc(u.nodes, func(n Node) bool { return n.UserID == id }) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": 9, "message": "user not empty: node(s) found"})
			return
		}
		u.userIDs = slices.DeleteFunc(u.userIDs, func(uid string) bool { return uid == id })
		writeJSON(w, http.StatusOK, map[string]any{})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
