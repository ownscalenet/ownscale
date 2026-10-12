package handlers

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"net/http"

	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
)

// funnelLabelRe matches a valid funnel-route label: alphanumeric only, no
// hyphens or dots. The label and org subdomain are joined by "-" into the
// single-DNS-label public funnel hostname "<label>-<org-subdomain>.<funnel domain>".
var funnelLabelRe = regexp.MustCompile(`^[a-z0-9]{1,63}$`)

func validateFunnelLabel(label string) string {
	if !funnelLabelRe.MatchString(label) {
		return "label must contain only lowercase letters and digits"
	}
	return ""
}

type funnelRouteResponse struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Hostname          string `json:"hostname"`
	TargetNodeID      string `json:"targetNodeId"`
	TargetNodeName    string `json:"targetNodeName,omitempty"`
	TargetNodeMissing bool   `json:"targetNodeMissing"`
	TargetPort        int64  `json:"targetPort"`
	CreatedAt         string `json:"createdAt"`
}

func (h *Handlers) toFunnelRouteResponse(route sqlcgen.FunnelRoute, subdomain string, nodesByID map[string]hscl.Node) funnelRouteResponse {
	resp := funnelRouteResponse{
		ID:           route.ID,
		Label:        route.Label,
		Hostname:     route.Label + "-" + subdomain + "." + h.funnelDomain,
		TargetNodeID: route.TargetNodeID,
		TargetPort:   route.TargetPort,
		CreatedAt:    route.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if node, ok := nodesByID[route.TargetNodeID]; ok {
		if node.GivenName != "" {
			resp.TargetNodeName = node.GivenName
		} else {
			resp.TargetNodeName = node.Name
		}
	} else {
		resp.TargetNodeMissing = true
	}
	return resp
}

// ListFunnelRoutes handles GET /api/funnel. With no client (see
// ReadHeadscale) there are no machines to describe the routes' targets.
func (h *Handlers) ListFunnelRoutes(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	client, org, _ := headscaleClientFromContext(r)

	routes, err := h.queries.ListFunnelRoutesByOrg(r.Context(), org.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	var nodes []hscl.Node
	if client != nil {
		if nodes, err = client.ListNodes(r.Context()); err != nil {
			h.serverError(w, r, err)
			return
		}
	}
	nodesByID := make(map[string]hscl.Node, len(nodes))
	for _, n := range nodes {
		nodesByID[n.ID] = n
	}

	resp := make([]funnelRouteResponse, 0, len(routes))
	for _, route := range routes {
		resp = append(resp, h.toFunnelRouteResponse(route, org.Subdomain, nodesByID))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"funnelRoutes": resp,
		"canManage":    isManager(caller.Role),
	})
}

// CreateFunnelRoute handles POST /api/funnel.
func (h *Handlers) CreateFunnelRoute(w http.ResponseWriter, r *http.Request) {
	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can create funnel routes")
		return
	}
	client, org, _ := headscaleClientFromContext(r)

	var req struct {
		Label        string `json:"label"`
		TargetNodeID string `json:"targetNodeId"`
		TargetPort   int    `json:"targetPort"`
	}
	if !h.decodeJSONBody(w, r, &req) {
		return
	}
	req.Label = strings.ToLower(strings.TrimSpace(req.Label))
	if msg := validateFunnelLabel(req.Label); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.TargetPort < 1 || req.TargetPort > 65535 {
		h.writeError(w, http.StatusBadRequest, "targetPort must be between 1 and 65535")
		return
	}
	if strings.TrimSpace(req.TargetNodeID) == "" {
		h.writeError(w, http.StatusBadRequest, "targetNodeId is required")
		return
	}

	targetNode, err := client.GetNode(r.Context(), req.TargetNodeID)
	if err != nil && !errors.Is(err, hscl.ErrNotFound) {
		h.serverError(w, r, err)
		return
	}
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "target machine not found")
		return
	}

	existing, err := h.queries.ListFunnelRoutesByOrg(r.Context(), org.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	for _, e := range existing {
		if e.Label == req.Label {
			h.writeError(w, http.StatusConflict, "label already in use")
			return
		}
	}

	if err := h.funnel.EnsureServer(r.Context(), org.Subdomain, client); err != nil {
		h.logger.Error("create funnel route: start tsnet server", "subdomain", org.Subdomain, "error", err)
		h.writeError(w, http.StatusBadGateway, "failed to start funnel server")
		return
	}

	created, err := h.queries.CreateFunnelRoute(r.Context(), sqlcgen.CreateFunnelRouteParams{
		ID:             xid.New().String(),
		OrganizationID: org.ID,
		Label:          req.Label,
		TargetNodeID:   req.TargetNodeID,
		TargetPort:     int64(req.TargetPort),
		CreatedBy:      sql.NullString{String: caller.ID, Valid: true},
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	nodesByID := map[string]hscl.Node{targetNode.ID: targetNode}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"funnelRoute": h.toFunnelRouteResponse(created, org.Subdomain, nodesByID),
	})
}

// DeleteFunnelRoute handles DELETE /api/funnel/{id}.
func (h *Handlers) DeleteFunnelRoute(w http.ResponseWriter, r *http.Request) {
	caller, org, r, ok := h.callerAndOrg(w, r)
	if !ok {
		return
	}
	if !isManager(caller.Role) {
		h.writeError(w, http.StatusForbidden, "only owners and admins can delete funnel routes")
		return
	}

	id := r.PathValue("id")
	if _, err := h.queries.GetFunnelRoute(r.Context(), sqlcgen.GetFunnelRouteParams{ID: id, OrganizationID: org.ID}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.writeError(w, http.StatusNotFound, "funnel route not found")
			return
		}
		h.serverError(w, r, err)
		return
	}

	if err := h.queries.DeleteFunnelRoute(r.Context(), sqlcgen.DeleteFunnelRouteParams{ID: id, OrganizationID: org.ID}); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
