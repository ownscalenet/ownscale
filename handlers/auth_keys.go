package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/headscaleclient"
)

// ListAuthKeys handles GET /api/auth-keys. It returns every
// pre-authentication key for the caller's organization's tailnet, plus the
// list of headscale users (so the frontend can build a user filter/picker
// without a second round trip). requireHeadscaleClient guarantees the
// client is reachable.
func (h *Handlers) ListAuthKeys(w http.ResponseWriter, r *http.Request) {
	client, _ := headscaleClientFromContext(r)

	keys, err := client.ListPreAuthKeys(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	users, err := client.ListUsers(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"authKeys": keys,
		"users":    users,
	})
}

// CreateAuthKey handles POST /api/auth-keys. The request body must set
// either userID (the owning headscale user) or a non-empty aclTags (for a
// "tag only" key with no owning user). requireHeadscaleClient guarantees
// the client is reachable.
func (h *Handlers) CreateAuthKey(w http.ResponseWriter, r *http.Request) {
	client, _ := headscaleClientFromContext(r)

	var req struct {
		UserID     string   `json:"userId"`
		ACLTags    []string `json:"aclTags"`
		ExpiryDays int      `json:"expiryDays"`
		Reusable   bool     `json:"reusable"`
		Ephemeral  bool     `json:"ephemeral"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.UserID) == "" && len(req.ACLTags) == 0 {
		h.writeError(w, http.StatusBadRequest, "either userId or aclTags is required")
		return
	}
	if req.ExpiryDays < 1 {
		h.writeError(w, http.StatusBadRequest, "expiryDays must be at least 1")
		return
	}

	key, err := client.CreatePreAuthKey(r.Context(), headscaleclient.CreatePreAuthKeyParams{
		UserID:     req.UserID,
		Reusable:   req.Reusable,
		Ephemeral:  req.Ephemeral,
		Expiration: time.Now().Add(time.Duration(req.ExpiryDays) * 24 * time.Hour),
		ACLTags:    req.ACLTags,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"authKey": key})
}

// ExpireAuthKey handles POST /api/auth-keys/{id}/expire.
// requireHeadscaleClient guarantees the client is reachable.
func (h *Handlers) ExpireAuthKey(w http.ResponseWriter, r *http.Request) {
	client, _ := headscaleClientFromContext(r)
	id := r.PathValue("id")

	if err := client.ExpirePreAuthKey(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
