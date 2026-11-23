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
	certs, err := h.orgs.ForOrg(org).NewCertsThisWeek(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	derp, err := h.orgs.ForOrg(org).GetDERPSettings(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"dns":               dns,
		"derp":              derp,
		"canManage":         isManager(caller.Role),
		"httpsCertificates": h.orgs.CertsEnabled(), // also needs MagicDNS
		"certsThisWeek":     certs,
		"certsPerWeek":      h.orgs.CertsPerOrgPerWeek(),
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

func (h *Handlers) UpdateDERPSettings(w http.ResponseWriter, r *http.Request) {
	caller, org, r, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can edit DERP settings")
		return
	}

	var req orgs.DERPSettings
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if err := h.orgs.ForOrg(org).SetDERPSettings(r.Context(), req); err != nil {
		if invalid, ok := errors.AsType[*orgs.InvalidConfigError](err); ok {
			h.writeError(w, http.StatusBadRequest, invalid.Error())
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ServeBuiltinDERPMap serves orgs.BuiltinDERPMap for Tailscale clients and
// other control servers.
func (h *Handlers) ServeBuiltinDERPMap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	h.writeJSON(w, http.StatusOK, orgs.BuiltinDERPMap())
}
