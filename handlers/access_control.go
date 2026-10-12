package handlers

import (
	"errors"
	"net/http"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/orgs"
	"github.com/tailscale/hujson"
)

// GetAccessControlPolicy handles GET /api/access-control. It returns the
// caller's organization's headscale ACL policy (HuJSON), plus whether the
// caller can edit it (owner/admin, mirroring ListMembers' canManage).
// With no client (see ReadHeadscale) it returns the default policy the
// organization's headscale will start with.
func (h *Handlers) GetAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	client, _, _ := headscaleClientFromContext(r)

	content := orgs.DefaultACLPolicy
	if client != nil {
		var err error
		if content, err = client.GetPolicy(r.Context()); err != nil {
			h.serverError(w, r, err)
			return
		}
	}
	// orgs.Service seeds a default policy when it starts an organization's
	// headscale, so an empty response here just means headscale genuinely
	// has no policy -- return it as-is, with no special-casing.
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
// also surfaced as a 400 rather than a 500. ProvisionHeadscale guarantees
// the org's headscale client is resolved.
func (h *Handlers) UpdateAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can edit the access control policy")
		return
	}
	client, _, _ := headscaleClientFromContext(r)

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
		h.writePolicyError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// CheckAccessControlPolicy handles POST /api/access-control/check. It
// validates content and runs its "tests" against the organization's current
// users and machines without saving, returning headscale's reason as a 400
// when the policy is rejected.
func (h *Handlers) CheckAccessControlPolicy(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can edit the access control policy")
		return
	}
	client, _, _ := headscaleClientFromContext(r)

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
	if err := client.CheckPolicy(r.Context(), req.Content); err != nil {
		h.writePolicyError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writePolicyError answers a 400 with headscale's own reason when it rejected
// the policy, and a 500 for any other failure.
func (h *Handlers) writePolicyError(w http.ResponseWriter, r *http.Request, err error) {
	if perr, ok := errors.AsType[*hscl.PolicyError](err); ok {
		h.writeError(w, http.StatusBadRequest, perr.Reason)
		return
	}
	h.serverError(w, r, err)
}
