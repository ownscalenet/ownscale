package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/headscaleclient"
)

// resolveMachinesClient returns the calling user's organization's
// headscaleclient.Client. notConfigured is true (nil client, nil error)
// when the user has no org, the org isn't active yet, or minting/reaching
// its headscale API key failed. Other failures are returned as err.
func (h *Handlers) resolveMachinesClient(r *http.Request) (_ *headscaleclient.Client, notConfigured bool, err error) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok || !user.OrganizationID.Valid {
		return nil, true, nil
	}

	org, err := h.queries.GetOrganization(r.Context(), user.OrganizationID.String)
	if err != nil {
		return nil, false, err
	}
	if org.Status != "active" {
		return nil, true, nil
	}

	client, err := h.hs.Client(r.Context(), org.Subdomain)
	if err != nil {
		h.logger.Warn("mint/reach headscale API client", "subdomain", org.Subdomain, "error", err)
		return nil, true, nil
	}
	return client, false, nil
}

// matchesFilters reports whether m satisfies the optional q (case
// insensitive substring match against name/givenName/IP addresses), user
// (exact match against the owning HeadscaleUser's name), and status
// ("online"/"offline"/"expired") filters.
func matchesFilters(m headscaleclient.Node, q, userFilter, status string) bool {
	if q != "" {
		q = strings.ToLower(q)
		matched := strings.Contains(strings.ToLower(m.Name), q) ||
			strings.Contains(strings.ToLower(m.GivenName), q)
		for _, ip := range m.IPAddresses {
			if strings.Contains(strings.ToLower(ip), q) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if userFilter != "" {
		if m.User == nil || m.User.Name != userFilter {
			return false
		}
	}
	if status != "" {
		expired := m.Expiry != nil && *m.Expiry != "" && isExpiredRFC3339(*m.Expiry)
		switch status {
		case "online":
			if !m.Online || expired {
				return false
			}
		case "offline":
			if m.Online || expired {
				return false
			}
		case "expired":
			if !expired {
				return false
			}
		}
	}
	return true
}

// ListMachines handles GET /api/machines. It returns every machine
// (headscale node) for the caller's organization, applying the optional
// q/user/status query-string filters. If headscale isn't reachable it
// responds 200 with notConfigured:true instead of an error.
func (h *Handlers) ListMachines(w http.ResponseWriter, r *http.Request) {
	client, notConfigured, err := h.resolveMachinesClient(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if notConfigured {
		h.writeJSON(w, http.StatusOK, map[string]any{
			"notConfigured": true,
			"machines":      []headscaleclient.Node{},
			"users":         []headscaleclient.User{},
			"total":         0,
		})
		return
	}

	nodes, err := client.ListNodes(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	users, err := client.ListUsers(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}

	q := r.URL.Query().Get("q")
	userFilter := r.URL.Query().Get("user")
	status := r.URL.Query().Get("status")

	filtered := make([]headscaleclient.Node, 0, len(nodes))
	for _, n := range nodes {
		if matchesFilters(n, q, userFilter, status) {
			filtered = append(filtered, n)
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"machines": filtered,
		"users":    users,
		"total":    len(nodes),
	})
}

// RegisterMachine handles POST /api/machines/register. It registers a
// machine that's waiting at its interactive registration key/URL (i.e.
// ran `tailscale up --login-server=...`) to the given user.
func (h *Handlers) RegisterMachine(w http.ResponseWriter, r *http.Request) {
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
		User string `json:"user"`
		Key  string `json:"key"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.User) == "" {
		h.writeError(w, http.StatusBadRequest, "user is required")
		return
	}
	key, err := headscaleclient.NormalizeRegistrationKey(req.Key)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	node, err := client.RegisterNode(r.Context(), req.User, key)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"machine": node})
}

// machineIDAndClient extracts the {id} path value and resolves the
// caller's organization's headscale client, writing an appropriate error
// response (and returning ok=false) if either isn't available.
func (h *Handlers) machineIDAndClient(w http.ResponseWriter, r *http.Request) (client *headscaleclient.Client, id string, ok bool) {
	id = r.PathValue("id")
	if id == "" {
		h.writeError(w, http.StatusBadRequest, "missing machine id")
		return nil, "", false
	}

	client, notConfigured, err := h.resolveMachinesClient(r)
	if err != nil {
		h.serverError(w, err)
		return nil, "", false
	}
	if notConfigured {
		h.writeError(w, http.StatusServiceUnavailable, "headscale is not reachable for this organization")
		return nil, "", false
	}
	return client, id, true
}

// decodeJSONBody decodes r's JSON body into v, writing a 400 response and
// returning false on failure.
func (h *Handlers) decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// RenameMachine handles POST /api/machines/{id}/rename.
func (h *Handlers) RenameMachine(w http.ResponseWriter, r *http.Request) {
	client, id, ok := h.machineIDAndClient(w, r)
	if !ok {
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
	if err := client.RenameNode(r.Context(), id, req.Name); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SetMachineTags handles POST /api/machines/{id}/tags.
func (h *Handlers) SetMachineTags(w http.ResponseWriter, r *http.Request) {
	client, id, ok := h.machineIDAndClient(w, r)
	if !ok {
		return
	}
	var req struct {
		Tags []string `json:"tags"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if err := client.SetTags(r.Context(), id, req.Tags); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SetMachineRoutes handles POST /api/machines/{id}/routes.
func (h *Handlers) SetMachineRoutes(w http.ResponseWriter, r *http.Request) {
	client, id, ok := h.machineIDAndClient(w, r)
	if !ok {
		return
	}
	var req struct {
		Routes []string `json:"routes"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if err := client.SetApprovedRoutes(r.Context(), id, req.Routes); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ExpireMachine handles POST /api/machines/{id}/expire.
func (h *Handlers) ExpireMachine(w http.ResponseWriter, r *http.Request) {
	client, id, ok := h.machineIDAndClient(w, r)
	if !ok {
		return
	}
	if err := client.ExpireNode(r.Context(), id); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteMachine handles DELETE /api/machines/{id}.
func (h *Handlers) DeleteMachine(w http.ResponseWriter, r *http.Request) {
	client, id, ok := h.machineIDAndClient(w, r)
	if !ok {
		return
	}
	if err := client.DeleteNode(r.Context(), id); err != nil {
		h.serverError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// isExpiredRFC3339 reports whether expiry (an RFC3339 timestamp, as
// produced by headscaleclient) is in the past. Unparseable timestamps are
// treated as not expired.
func isExpiredRFC3339(expiry string) bool {
	t, err := time.Parse(time.RFC3339, expiry)
	if err != nil {
		return false
	}
	return t.Before(time.Now())
}
