package handlers

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/hscl"
)

// ListAuthKeys handles GET /api/auth-keys. It returns the
// pre-authentication keys for the caller's organization's tailnet (only
// their own for members), the headscale users for filtering, and the
// members the caller may create keys for.
func (h *Handlers) ListAuthKeys(w http.ResponseWriter, r *http.Request) {
	client, _, _ := headscaleClientFromContext(r)
	caller, _ := auth.UserFromContext(r.Context())

	assignable, err := h.assignableUsers(r.Context(), caller)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
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

	canManage := caller != nil && isManager(caller.Role)
	if !canManage {
		keys = slices.DeleteFunc(keys, func(k hscl.PreAuthKey) bool {
			return caller == nil || k.User == nil || !ownsHeadscaleUser(caller, k.User.ID)
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"authKeys":        keys,
		"users":           users,
		"assignableUsers": assignable,
		"canManage":       canManage,
	})
}

// CreateAuthKey handles POST /api/auth-keys. The request body must set
// either memberId (the member owning the key) or a non-empty aclTags (for
// a "tag only" key with no owning user). Members may only create untagged
// keys for themselves. RequireHeadscaleClient guarantees the client is
// reachable.
func (h *Handlers) CreateAuthKey(w http.ResponseWriter, r *http.Request) {
	client, org, _ := headscaleClientFromContext(r)

	var req struct {
		MemberID   string   `json:"memberId"`
		ACLTags    []string `json:"aclTags"`
		ExpiryDays int      `json:"expiryDays"`
		Reusable   bool     `json:"reusable"`
		Ephemeral  bool     `json:"ephemeral"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.MemberID) == "" && len(req.ACLTags) == 0 {
		h.writeError(w, http.StatusBadRequest, "either memberId or aclTags is required")
		return
	}
	if req.ExpiryDays < 1 {
		h.writeError(w, http.StatusBadRequest, "expiryDays must be at least 1")
		return
	}
	if caller, _ := auth.UserFromContext(r.Context()); caller == nil || !isManager(caller.Role) {
		if caller == nil || req.MemberID != caller.ID || len(req.ACLTags) > 0 {
			h.writeError(w, http.StatusForbidden, "you can only create untagged keys for yourself")
			return
		}
	}
	var hsUserID string
	if req.MemberID != "" {
		hsUser, ok := h.requireMemberHeadscaleUser(w, r, client, org, req.MemberID)
		if !ok {
			return
		}
		hsUserID = hsUser.ID
	}

	key, err := client.CreatePreAuthKey(r.Context(), hscl.CreatePreAuthKeyParams{
		UserID:     hsUserID,
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
	client, _, _ := headscaleClientFromContext(r)
	id := r.PathValue("id")
	if !h.requireKeyAccess(w, r, client, id) {
		return
	}

	if err := client.ExpirePreAuthKey(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteAuthKey handles DELETE /api/auth-keys/{id}.
// requireHeadscaleClient guarantees the client is reachable.
func (h *Handlers) DeleteAuthKey(w http.ResponseWriter, r *http.Request) {
	client, _, _ := headscaleClientFromContext(r)
	id := r.PathValue("id")
	if !h.requireKeyAccess(w, r, client, id) {
		return
	}

	if err := client.DeletePreAuthKey(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// requireKeyAccess writes a 403 and returns false unless the caller is a
// manager or owns pre-auth key id.
func (h *Handlers) requireKeyAccess(w http.ResponseWriter, r *http.Request, client *hscl.Client, id string) bool {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return false
	}
	if isManager(caller.Role) {
		return true
	}
	keys, err := client.ListPreAuthKeys(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return false
	}
	// TODO: any way to filter these not manually?
	for _, k := range keys {
		if k.ID == id && k.User != nil && ownsHeadscaleUser(caller, k.User.ID) {
			return true
		}
	}
	h.writeError(w, http.StatusForbidden, "you can only manage your own pre-auth keys")
	return false
}
