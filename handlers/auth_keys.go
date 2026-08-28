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
// without a second round trip). If headscale isn't reachable for the
// caller's organization it responds 200 with notConfigured:true and empty
// lists, matching ListMachines's convention.
func (h *Handlers) ListAuthKeys(w http.ResponseWriter, r *http.Request) {
	client, notConfigured, err := h.resolveMachinesClient(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if notConfigured {
		h.writeJSON(w, http.StatusOK, map[string]any{
			"notConfigured": true,
			"authKeys":      []headscaleclient.PreAuthKey{},
			"users":         []headscaleclient.User{},
		})
		return
	}

	keys, err := client.ListPreAuthKeys(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	users, err := client.ListUsers(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"authKeys": keys,
		"users":    users,
	})
}

// CreateAuthKey handles POST /api/auth-keys. The request body must set
// either userID (the owning headscale user) or a non-empty aclTags (for a
// "tag only" key with no owning user).
func (h *Handlers) CreateAuthKey(w http.ResponseWriter, r *http.Request) {
	client, notConfigured, err := h.resolveMachinesClient(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if notConfigured {
		h.writeError(w, http.StatusServiceUnavailable, "headscale is not reachable for this organization")
		return
	}

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
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"authKey": key})
}

// ExpireAuthKey handles POST /api/auth-keys/{id}/expire.
func (h *Handlers) ExpireAuthKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		h.writeError(w, http.StatusBadRequest, "missing auth key id")
		return
	}

	client, notConfigured, err := h.resolveMachinesClient(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if notConfigured {
		h.writeError(w, http.StatusServiceUnavailable, "headscale is not reachable for this organization")
		return
	}

	if err := client.ExpirePreAuthKey(r.Context(), id); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
