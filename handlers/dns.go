package handlers

import (
	"errors"
	"net/http"

	"github.com/ownscalenet/ownscale/orgs"
)

func (h *Handlers) GetDNSSettings(w http.ResponseWriter, r *http.Request) {
	caller, org, r, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}

	dns, err := h.orgs.ForOrg(org).GetDNSSettings(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"dns":       dns,
		"canManage": isManager(caller.Role),
	})
}

func (h *Handlers) UpdateDNSSettings(w http.ResponseWriter, r *http.Request) {
	caller, org, r, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can edit DNS settings")
		return
	}

	var req orgs.DNSConfig
	if !h.decodeJSONBody(w, r, &req) {
		return
	}

	err := h.orgs.ForOrg(org).SetDNSSettings(r.Context(), req)
	// Because headscale got restarted, funnel machine keeps connections to the old one.
	h.funnel.Evict(org.Subdomain)
	if err != nil {
		if invalid, ok := errors.AsType[*orgs.InvalidConfigError](err); ok {
			h.writeError(w, http.StatusBadRequest, invalid.Error())
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
