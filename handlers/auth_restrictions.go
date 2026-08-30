package handlers

import (
	"net/http"
	"strings"
)

// GetAuthRestrictions handles GET /api/auth-restrictions. It returns the
// caller's organization's stored OIDC login allowlist (domains, groups,
// users), plus whether the caller can edit it (owner/admin, mirroring
// GetAccessControlPolicy's canManage).
func (h *Handlers) GetAuthRestrictions(w http.ResponseWriter, r *http.Request) {
	caller, org, notConfigured, ok := h.resolveActiveOrg(w, r)
	if !ok {
		return
	}
	if notConfigured {
		h.writeJSON(w, http.StatusOK, map[string]any{
			"notConfigured": true,
			"domains":       []string{},
			"groups":        []string{},
			"users":         []string{},
			"canManage":     isManager(caller.Role),
		})
		return
	}

	domains, groups, users, err := h.orgs.GetAuthRestrictions(r.Context(), org.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"domains":   emptyIfNil(domains),
		"groups":    emptyIfNil(groups),
		"users":     emptyIfNil(users),
		"canManage": isManager(caller.Role),
	})
}

// emptyIfNil returns a non-nil empty slice for a nil input, so JSON
// encodes an empty list ("[]") rather than null.
func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// normalizeAuthRestrictionList trims whitespace from each entry, drops
// blanks, and deduplicates while preserving first-seen order (mirroring
// headplane's [...new Set(...)] behavior for its allowed_domains/groups/
// users lists).
func normalizeAuthRestrictionList(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// UpdateAuthRestrictions handles PUT /api/auth-restrictions. Only an
// owner/admin may edit the OIDC login allowlist. Each of domains/groups/
// users is trimmed, deduplicated, and persisted+applied as a whole
// (replacing whatever was there before), mirroring
// UpdateAccessControlPolicy's full-content-replace semantics.
func (h *Handlers) UpdateAuthRestrictions(w http.ResponseWriter, r *http.Request) {
	caller, org, notConfigured, ok := h.resolveActiveOrg(w, r)
	if !ok {
		return
	}
	if notConfigured {
		h.writeError(w, http.StatusServiceUnavailable, "organization is not active yet")
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can edit authentication restrictions")
		return
	}

	var req struct {
		Domains []string `json:"domains"`
		Groups  []string `json:"groups"`
		Users   []string `json:"users"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}

	domains := normalizeAuthRestrictionList(req.Domains)
	groups := normalizeAuthRestrictionList(req.Groups)
	users := normalizeAuthRestrictionList(req.Users)

	if err := h.orgs.SetAuthRestrictions(r.Context(), org.ID, org.Subdomain, domains, groups, users); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"domains": emptyIfNil(domains),
		"groups":  emptyIfNil(groups),
		"users":   emptyIfNil(users),
	})
}
