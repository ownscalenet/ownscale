package handlers

import (
	"net/http"

	"github.com/tailscale/hujson"
)

// GetAccessControlPolicy handles GET /api/access-control. It returns the
// caller's organization's headscale ACL policy (HuJSON), plus whether the
// caller can edit it (owner/admin, mirroring ListMembers' canManage).
// RequireActiveOrg/RequireHeadscaleClient guarantee the org is active and
// its headscale client is resolved.
func (h *Handlers) GetAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	caller, _, _ := orgFromContext(r)
	client, _ := headscaleClientFromContext(r)

	content, err := client.GetPolicy(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	// dockerctl.CreateOrganization seeds every organization with a default
	// policy via headscale's own API at provisioning time, so an empty
	// response here just means headscale genuinely has no policy -- return
	// it as-is, with no special-casing.
	h.writeJSON(w, http.StatusOK, map[string]any{
		"content":   content,
		"canManage": isManager(caller.Role),
	})
}

// UpdateAccessControlPolicy handles PUT /api/access-control. Only an
// owner/admin may edit the ACL policy. content is validated as HuJSON
// before anything is sent to headscale, so a malformed policy is rejected
// with a 400 and a parser error message; headscale itself may still reject
// a syntactically valid policy (e.g. bad tag/user references), which is
// also surfaced as a 400 rather than a 500. RequireActiveOrg/
// RequireHeadscaleClient guarantee the org is active and its headscale
// client is resolved.
func (h *Handlers) UpdateAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	caller, _, _ := orgFromContext(r)
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can edit the access control policy")
		return
	}
	client, _ := headscaleClientFromContext(r)

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

	if err := client.SetPolicy(r.Context(), req.Content); err != nil {
		h.writeError(w, http.StatusBadRequest, "headscale rejected policy: "+err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
