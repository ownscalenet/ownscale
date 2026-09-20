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
	headscaleClientContextKey orgContextKey = iota
	requestLoggerContextKey
)

// headscaleContext is the (client, org) pair attached to the request
// context by RequireHeadscaleClient.
type headscaleContext struct {
	client *headscaleclient.Client
	org    sqlcgen.Organization
}

// headscaleClientFromContext returns the client and org attached by
// RequireHeadscaleClient.
func headscaleClientFromContext(r *http.Request) (client *headscaleclient.Client, org sqlcgen.Organization, ok bool) {
	c, ok := r.Context().Value(headscaleClientContextKey).(headscaleContext)
	return c.client, c.org, ok
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
		ctx := context.WithValue(r.Context(), headscaleClientContextKey, headscaleContext{client: client, org: org})
		next(w, r.WithContext(ctx))
	}
}
