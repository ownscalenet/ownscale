package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/orgs"
	"github.com/tailscale/hujson"
)

// resolveHeadscaleClient returns the calling user's organization's
// hscl.Client, along with the resolved org. notConfigured is true (nil
// client, nil error) when its headscale isn't running, or never started.
func (h *Handlers) resolveHeadscaleClient(r *http.Request) (_ *hscl.Client, org sqlcgen.Organization, notConfigured bool, err error) {
	client, org, err := h.resolveHeadscale(r)
	if errors.Is(err, errNoHeadscale) {
		return nil, org, true, nil
	}
	return client, org, false, err
}

// errNoHeadscale wraps the reason resolveHeadscale has no client: the
// caller has no organization, or its headscale isn't running, which
// includes orgs.ErrNotProvisioned.
var errNoHeadscale = errors.New("no headscale client")

// resolveHeadscale returns the calling user's organization and its
// headscale client. Without a client, the error wraps errNoHeadscale.
func (h *Handlers) resolveHeadscale(r *http.Request) (_ *hscl.Client, org sqlcgen.Organization, err error) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		return nil, sqlcgen.Organization{}, errNoHeadscale
	}
	org, err = h.queries.GetOrganization(r.Context(), user.OrganizationID)
	if err != nil {
		return nil, sqlcgen.Organization{}, err
	}
	client, err := h.hs.Wake(r.Context(), org.Subdomain)
	if err != nil {
		if !errors.Is(err, orgs.ErrNotProvisioned) {
			h.logger.With("org", org.Subdomain).Warn("get headscale client", "error", err)
		}
		return nil, org, fmt.Errorf("%w: %w", errNoHeadscale, err)
	}
	return client, org, nil
}

// matchesFilters reports whether m satisfies the optional q (case
// insensitive substring match against name/givenName/IP addresses), user
// (exact match against the owning HeadscaleUser's name), and status
// ("online"/"offline"/"expired") filters.
func matchesFilters(m hscl.Node, q, userFilter, status string) bool {
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
		expired := m.Expired
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

type machineResponse struct {
	hscl.Node
	Internal bool `json:"internal"`
	// CanEdit reports whether the caller may manage this machine.
	CanEdit bool `json:"canEdit"`
}

func toMachineResponse(n hscl.Node, caller *sqlcgen.User) machineResponse {
	return machineResponse{
		Node:     n,
		Internal: funnelproxy.IsFunnelUser(n.User),
		CanEdit: caller != nil &&
			(isManager(caller.Role) || n.User != nil && ownsHeadscaleUser(caller, n.User.ID)),
	}
}

// ListMachines handles GET /api/machines. It returns every machine
// (headscale node) for the caller's organization, applying the optional
// q/user/status query-string filters, plus the headscale users and the
// members the caller may register machines for. With no client (see
// ReadHeadscale) there are no machines or headscale users yet.
func (h *Handlers) ListMachines(w http.ResponseWriter, r *http.Request) {
	client, _, _ := headscaleClientFromContext(r)
	caller, _ := auth.UserFromContext(r.Context())

	assignable, err := h.assignableUsers(r.Context(), caller)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	nodes, users := []hscl.Node{}, []hscl.User{}
	if client != nil {
		if nodes, err = client.ListNodes(r.Context()); err != nil {
			h.serverError(w, r, err)
			return
		}
		if users, err = client.ListUsers(r.Context()); err != nil {
			h.serverError(w, r, err)
			return
		}
	}

	q := r.URL.Query().Get("q")
	userFilter := r.URL.Query().Get("user")
	status := r.URL.Query().Get("status")

	filtered := make([]machineResponse, 0, len(nodes))
	for _, n := range nodes {
		if matchesFilters(n, q, userFilter, status) {
			filtered = append(filtered, toMachineResponse(n, caller))
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"machines":        filtered,
		"users":           users,
		"assignableUsers": assignable,
		"total":           len(nodes),
		"canManage":       caller != nil && isManager(caller.Role),
	})
}

// RegisterMachine handles POST /api/machines/register. It registers a
// machine that's waiting at its interactive registration key/URL (i.e.
// ran `tailscale up --login-server=...`) to the member memberId. Members
// may only register machines for themselves. ProvisionHeadscale guarantees
// the client is reachable.
func (h *Handlers) RegisterMachine(w http.ResponseWriter, r *http.Request) {
	client, org, _ := headscaleClientFromContext(r)

	var req struct {
		MemberID string `json:"memberId"`
		Key      string `json:"key"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.MemberID) == "" {
		h.writeError(w, http.StatusBadRequest, "memberId is required")
		return
	}
	key, err := hscl.NormalizeRegistrationKey(req.Key)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if caller, _ := auth.UserFromContext(r.Context()); !canAssignTo(caller, req.MemberID) {
		h.writeError(w, http.StatusForbidden, "you can only register machines for yourself")
		return
	}
	hsUser, ok := h.requireMemberHeadscaleUser(w, r, client, org, req.MemberID)
	if !ok {
		return
	}

	node, err := client.RegisterNode(r.Context(), hsUser.Name, key)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"machine": node})
}

// requireMemberHeadscaleUser is memberHeadscaleUser, writing the error
// response and returning false on failure.
func (h *Handlers) requireMemberHeadscaleUser(w http.ResponseWriter, r *http.Request, client *hscl.Client, org sqlcgen.Organization, memberID string) (hscl.User, bool) {
	hsUser, err := h.memberHeadscaleUser(r.Context(), client, org, memberID)
	if errors.Is(err, errMemberNotFound) {
		h.writeError(w, http.StatusNotFound, "member not found")
		return hscl.User{}, false
	}
	if err != nil {
		h.serverError(w, r, err)
		return hscl.User{}, false
	}
	return hsUser, true
}

// machineIDAndClient extracts the {id} path value and the caller's
// organization's headscale client (attached by requireHeadscaleClient).
func (h *Handlers) machineIDAndClient(r *http.Request) (client *hscl.Client, id string) {
	client, _, _ = headscaleClientFromContext(r)
	return client, r.PathValue("id")
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

func (h *Handlers) decodeJSONBodyOptional(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// RenameMachine handles POST /api/machines/{id}/rename.
func (h *Handlers) RenameMachine(w http.ResponseWriter, r *http.Request) {
	client, id := h.machineIDAndClient(r)
	if !h.requireNodeAccess(w, r, client, id) {
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
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// normalizeTags trims, lower-cases, prepends the required "tag:" prefix if
// missing, drops empties, and de-duplicates.
// TODO: could this use a typed struct? What about any comments in the file?
func normalizeTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "tag:") {
			t = "tag:" + t
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ensureTagOwners fetches client's ACL policy and adds any of tags not
// already declared as a "tagOwners" key. It edits the policy's HuJSON AST.
func ensureTagOwners(ctx context.Context, client *hscl.Client, tags []string) (err error) {
	content, err := client.GetPolicy(ctx)
	if err != nil {
		return err
	}

	root, err := hujson.Parse([]byte(content))
	if err != nil {
		return err
	}

	existing := map[string]bool{}
	tagOwnersExists := false
	if tagOwners := root.Find("/tagOwners"); tagOwners != nil {
		tagOwnersExists = true
		if obj, ok := tagOwners.Value.(*hujson.Object); ok {
			for _, m := range obj.Members {
				if name, ok := m.Name.Value.(hujson.Literal); ok {
					existing[name.String()] = true
				}
			}
		}
	}

	missing := make([]string, 0, len(tags))
	for _, tag := range tags {
		if !existing[tag] {
			missing = append(missing, tag)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	users, err := client.ListUsers(ctx)
	if err != nil {
		return err
	}
	// headscale's ACL policy requires bare (domain-less) usernames to be
	// written with a trailing "@" (e.g. "alice@"), stripped again before
	// matching against the user's Name/Email.
	owners := make([]string, 0, len(users))
	for _, u := range users {
		if u.Name != "" {
			owners = append(owners, u.Name+"@")
		}
	}
	if len(owners) == 0 {
		return errors.New("no headscale users available to own new tags")
	}

	type patchOp struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value,omitempty"`
	}
	var ops []patchOp
	if !tagOwnersExists {
		ops = append(ops, patchOp{Op: "add", Path: "/tagOwners", Value: map[string]any{}})
	}
	for _, tag := range missing {
		ops = append(ops, patchOp{Op: "add", Path: "/tagOwners/" + jsonPointerEscape(tag), Value: owners})
	}

	patch, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	if err := root.Patch(patch); err != nil {
		return err
	}
	root.Format()

	return client.SetPolicy(ctx, root.String())
}

// jsonPointerEscape escapes s for use as a single JSON Pointer (RFC 6901)
// reference token.
func jsonPointerEscape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

// SetMachineTags handles POST /api/machines/{id}/tags.
func (h *Handlers) SetMachineTags(w http.ResponseWriter, r *http.Request) {
	// Tagging can add tagOwners to the ACL policy.
	if !h.requireManager(w, r, "only owners and admins can tag machines") {
		return
	}
	client, id := h.machineIDAndClient(r)
	var req struct {
		Tags []string `json:"tags"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	tags := normalizeTags(req.Tags)
	if len(tags) > 0 {
		if err := ensureTagOwners(r.Context(), client, tags); err != nil {
			h.writeError(w, http.StatusBadRequest, "failed to update ACL policy for new tags: "+err.Error())
			return
		}
	}
	if err := client.SetTags(r.Context(), id, tags); err != nil {
		h.writeError(w, http.StatusBadRequest, "headscale rejected tags: "+err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SetMachineRoutes handles POST /api/machines/{id}/routes.
func (h *Handlers) SetMachineRoutes(w http.ResponseWriter, r *http.Request) {
	// Approving routes exposes networks to the whole tailnet.
	if !h.requireManager(w, r, "only owners and admins can approve routes") {
		return
	}
	client, id := h.machineIDAndClient(r)
	var req struct {
		Routes []string `json:"routes"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	if err := client.SetApprovedRoutes(r.Context(), id, req.Routes); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ExpireMachine handles POST /api/machines/{id}/expire.
func (h *Handlers) ExpireMachine(w http.ResponseWriter, r *http.Request) {
	client, id := h.machineIDAndClient(r)
	if !h.requireNodeAccess(w, r, client, id) {
		return
	}
	if err := client.ExpireNode(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteMachine handles DELETE /api/machines/{id}. If the machine is a funnel,
// this also deletes every funnel route for the org.
func (h *Handlers) DeleteMachine(w http.ResponseWriter, r *http.Request) {
	client, id := h.machineIDAndClient(r)
	if !h.requireNodeAccess(w, r, client, id) {
		return
	}

	if isFunnelMachine, err := h.isFunnelMachine(r.Context(), client, id); err != nil {
		h.serverError(w, r, err)
		return
	} else if isFunnelMachine {
		user, ok := auth.UserFromContext(r.Context())
		if !ok {
			h.writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err := h.queries.DeleteFunnelRoutesByOrg(r.Context(), user.OrganizationID); err != nil {
			h.serverError(w, r, err)
			return
		}
	}

	if err := client.DeleteNode(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handlers) isFunnelMachine(ctx context.Context, client *hscl.Client, nodeID string) (bool, error) {
	nodes, err := client.ListNodes(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range nodes {
		if n.ID == nodeID {
			return isFunnelNode(n), nil
		}
	}
	return false, nil
}
