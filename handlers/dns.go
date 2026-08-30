package handlers

import (
	"errors"
	"net/http"

	"github.com/ownscalenet/ownscale/dockerctl"
)

func (h *Handlers) GetDNSSettings(w http.ResponseWriter, r *http.Request) {
	caller, org, _ := orgFromContext(r)

	dns, err := h.orgs.ForOrg(org).GetDNSSettings(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"dns":       dns,
		"canManage": isManager(caller.Role),
	})
}

func (h *Handlers) UpdateDNSSettings(w http.ResponseWriter, r *http.Request) {
	caller, org, _ := orgFromContext(r)
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can edit DNS settings")
		return
	}

	var req dockerctl.DNSConfig
	if !h.decodeJSONBody(w, r, &req) {
		return
	}

	if err := h.orgs.ForOrg(org).SetDNSSettings(r.Context(), req); err != nil {
		if invalid, ok := errors.AsType[*dockerctl.InvalidConfigError](err); ok {
			h.writeError(w, http.StatusBadRequest, invalid.Error())
			return
		}
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
