package handlers

import (
	"net/http"
	"strings"
)

// ListHeadscaleUsers handles GET /api/headscale-users. It returns every
// headscale user known to the caller's organization (i.e. every possible
// machine owner), the same set ListMachines exposes, so the Users page's
// headscale-users table and member-linking dropdown can share one
// endpoint. Mirrors ListMachines' "not configured" handling: if headscale
// isn't reachable yet it responds 200 with an empty list rather than an
// error.
func (h *Handlers) ListHeadscaleUsers(w http.ResponseWriter, r *http.Request) {
	client, notConfigured, err := h.resolveMachinesClient(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if notConfigured {
		h.writeJSON(w, http.StatusOK, map[string]any{
			"notConfigured": true,
			"users":         []any{},
		})
		return
	}

	users, err := client.ListUsers(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// CreateHeadscaleUser handles POST /api/headscale-users. Admin/owner only.
func (h *Handlers) CreateHeadscaleUser(w http.ResponseWriter, r *http.Request) {
	caller, _, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can create headscale users")
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
	var req struct {
		Name string `json:"name"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	user, err := client.CreateUser(r.Context(), req.Name)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

// RenameHeadscaleUser handles POST /api/headscale-users/{id}/rename.
// Admin/owner only.
func (h *Handlers) RenameHeadscaleUser(w http.ResponseWriter, r *http.Request) {
	caller, _, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can rename headscale users")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		h.writeError(w, http.StatusBadRequest, "missing user id")
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
	var req struct {
		Name string `json:"name"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := client.RenameUser(r.Context(), id, req.Name); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteHeadscaleUser handles DELETE /api/headscale-users/{id}. Admin/owner
// only; refuses (409) if the user still owns any machines, mirroring
// headplane's guard against orphaning nodes.
func (h *Handlers) DeleteHeadscaleUser(w http.ResponseWriter, r *http.Request) {
	caller, _, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can delete headscale users")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		h.writeError(w, http.StatusBadRequest, "missing user id")
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

	nodes, err := client.ListNodes(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	for _, n := range nodes {
		if n.User != nil && n.User.ID == id {
			h.writeError(w, http.StatusConflict, "this user still owns machines; reassign or remove them first")
			return
		}
	}

	if err := client.DeleteUser(r.Context(), id); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
