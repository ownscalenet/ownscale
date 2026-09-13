package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/headscaleclient"
)

type orgContextKey int

const (
	activeOrgContextKey orgContextKey = iota
	headscaleClientContextKey
	requestLoggerContextKey
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

// headscaleClientFromContext returns the client attached by
// RequireHeadscaleClient.
func headscaleClientFromContext(r *http.Request) (*headscaleclient.Client, bool) {
	c, ok := r.Context().Value(headscaleClientContextKey).(*headscaleclient.Client)
	return c, ok
}

// withOrgLogger returns ctx carrying a logger annotated with the
// organization's subdomain (field "org"), for any handler or callee that
// resolves an organization and wants its logging -- including
// h.serverError -- to be attributable to that organization.
func (h *Handlers) withOrgLogger(ctx context.Context, subdomain string) context.Context {
	return context.WithValue(ctx, requestLoggerContextKey, h.logger.With("org", subdomain))
}

// loggerFromContext returns the logger attached by withOrgLogger, or
// h.logger if the request never resolved an organization (or resolution
// failed before reaching that point).
func (h *Handlers) loggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(requestLoggerContextKey).(*slog.Logger); ok {
		return l
	}
	return h.logger
}

// RequireActiveOrg wraps next so it only runs once the caller's org is
// resolved and active, short-circuiting with 409+notConfigured otherwise.
// This is used for endpoints backed by the org's own DB row (access
// control, auth restrictions) rather than headscale itself.
func (h *Handlers) RequireActiveOrg(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, org, r, notConfigured, ok := h.resolveActiveOrg(w, r)
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

// RequireHeadscaleClient wraps next so it only runs once the caller's
// organization's headscale client is resolved, short-circuiting with
// 409+notConfigured if the org isn't active or headscale isn't reachable.
func (h *Handlers) RequireHeadscaleClient(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client, org, notConfigured, err := h.resolveHeadscaleClient(r)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		if org.Subdomain != "" {
			r = r.WithContext(h.withOrgLogger(r.Context(), org.Subdomain))
		}
		if notConfigured {
			h.writeJSON(w, http.StatusConflict, map[string]bool{"notConfigured": true})
			return
		}
		ctx := context.WithValue(r.Context(), headscaleClientContextKey, client)
		next(w, r.WithContext(ctx))
	}
}
