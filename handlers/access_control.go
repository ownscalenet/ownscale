package handlers

import (
	"net/http"

	"github.com/tailscale/hujson"
)

// GetAccessControlPolicy handles GET /api/access-control. It returns the
// caller's organization's stored headscale ACL policy (HuJSON), plus
// whether the caller can edit it (owner/admin, mirroring ListMembers'
// canManage).
func (h *Handlers) GetAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	caller, org, notConfigured, ok := h.resolveActiveOrg(w, r)
	if !ok {
		return
	}
	if notConfigured {
		h.writeJSON(w, http.StatusOK, map[string]any{
			"notConfigured": true,
			"content":       "",
			"canManage":     isManager(caller.Role),
		})
		return
	}

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
func (h *Handlers) UpdateAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	caller, org, notConfigured, ok := h.resolveActiveOrg(w, r)
	if !ok {
		return
	}
	if notConfigured {
		h.writeError(w, http.StatusServiceUnavailable, "organization is not active yet")
		return
	}
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

// FormatAccessControlPolicy handles POST /api/access-control/format. It
// pretty-prints the supplied HuJSON content using hujson.Format, which
// preserves comments. This is a stateless transform with no DB write or
// Docker interaction, so it only requires the caller to be authenticated
// (no organization/manager gating, unlike Get/UpdateAccessControlPolicy).
func (h *Handlers) FormatAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}

	formatted, err := hujson.Format([]byte(req.Content))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid HuJSON: "+err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"content": string(formatted)})
}
