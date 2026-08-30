package handlers

import (
	"net/http"

	"github.com/tailscale/hujson"
)

// GetAccessControlPolicy handles GET /api/access-control. It returns the
// caller's organization's stored headscale ACL policy (HuJSON), plus
// whether the caller can edit it (owner/admin, mirroring ListMembers'
// canManage). requireActiveOrg guarantees the org is active.
func (h *Handlers) GetAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	caller, org, _ := orgFromContext(r)

	content, err := h.orgs.GetACLPolicy(r.Context(), org.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"content":   content,
		"canManage": isManager(caller.Role),
	})
}

// UpdateAccessControlPolicy handles PUT /api/access-control. Only an
// owner/admin may edit the ACL policy. content is validated as HuJSON
// before anything is persisted, so a malformed policy is rejected with a
// 400 and a parser error message rather than reaching orgs.Service (which
// re-validates as a safety net) or ever being uploaded to headscale.
// requireActiveOrg guarantees the org is active.
func (h *Handlers) UpdateAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	caller, org, _ := orgFromContext(r)
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can edit the access control policy")
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

	if err := h.orgs.SetACLPolicy(r.Context(), org.ID, org.Subdomain, req.Content); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
