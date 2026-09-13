package handlers

import (
	"net/http"

	"github.com/tailscale/hujson"
)

// GetAuthRestrictions handles GET /api/auth-restrictions. It returns the
// caller's organization's stored OIDC login allowlist (HuJSON), plus
// whether the caller can edit it (owner/admin, mirroring
// GetAccessControlPolicy's canManage). requireActiveOrg guarantees the
// org is active.
func (h *Handlers) GetAuthRestrictions(w http.ResponseWriter, r *http.Request) {
	caller, org, _ := orgFromContext(r)

	content, err := h.orgs.ForOrg(org).GetAuthRestrictions(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"content":   content,
		"canManage": isManager(caller.Role),
	})
}

// UpdateAuthRestrictions handles PUT /api/auth-restrictions. Only an
// owner/admin may edit the OIDC login allowlist. content is validated as
// HuJSON before anything is persisted, mirroring
// UpdateAccessControlPolicy's validate-then-persist semantics.
// requireActiveOrg guarantees the org is active.
func (h *Handlers) UpdateAuthRestrictions(w http.ResponseWriter, r *http.Request) {
	caller, org, _ := orgFromContext(r)
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can edit authentication restrictions")
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if _, err := hujson.Parse([]byte(req.Content)); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid HuJSON: "+err.Error())
		return
	}

	if err := h.orgs.ForOrg(org).SetAuthRestrictions(r.Context(), req.Content); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
