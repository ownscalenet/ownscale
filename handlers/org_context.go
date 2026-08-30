package handlers

import (
	"context"
	"net/http"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/headscaleclient"
)

type orgContextKey int

const (
	activeOrgContextKey orgContextKey = iota
	machinesClientContextKey
)

// activeOrgContext is the (caller, org) pair attached to the request
// context by RequireActiveOrg.
type activeOrgContext struct {
	caller *sqlcgen.User
	org    sqlcgen.Organization
}

// orgFromContext returns the caller and org attached by RequireActiveOrg.
func orgFromContext(r *http.Request) (caller *sqlcgen.User, org sqlcgen.Organization, ok bool) {
	c, ok := r.Context().Value(activeOrgContextKey).(activeOrgContext)
	return c.caller, c.org, ok
}

// machinesClientFromContext returns the client attached by
// RequireMachinesClient.
func machinesClientFromContext(r *http.Request) (*headscaleclient.Client, bool) {
	c, ok := r.Context().Value(machinesClientContextKey).(*headscaleclient.Client)
	return c, ok
}

// RequireActiveOrg wraps next so it only runs once the caller's org is
// resolved and active, short-circuiting with 409+notConfigured otherwise.
// This is used for endpoints backed by the org's own DB row (access
// control, auth restrictions) rather than headscale itself.
func (h *Handlers) RequireActiveOrg(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, org, notConfigured, ok := h.resolveActiveOrg(w, r)
		if !ok {
			return
		}
		if notConfigured {
			h.writeJSON(w, http.StatusConflict, map[string]bool{"notConfigured": true})
			return
		}
		ctx := context.WithValue(r.Context(), activeOrgContextKey, activeOrgContext{caller: caller, org: org})
		next(w, r.WithContext(ctx))
	}
}

// RequireMachinesClient wraps next so it only runs once the caller's
// organization's headscale client is resolved, short-circuiting with
// 409+notConfigured if the org isn't active or headscale isn't reachable.
func (h *Handlers) RequireMachinesClient(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client, notConfigured, err := h.resolveMachinesClient(r)
		if err != nil {
			h.serverError(w, err)
			return
		}
		if notConfigured {
			h.writeJSON(w, http.StatusConflict, map[string]bool{"notConfigured": true})
			return
		}
		ctx := context.WithValue(r.Context(), machinesClientContextKey, client)
		next(w, r.WithContext(ctx))
	}
}
