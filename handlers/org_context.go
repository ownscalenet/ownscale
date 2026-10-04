package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/orgs"
)

type orgContextKey int

const (
	headscaleClientContextKey orgContextKey = iota
	requestLoggerContextKey
)

// headscaleContext is the (client, org) pair attached to the request
// context by RequireHeadscaleClient.
type headscaleContext struct {
	client *hscl.Client
	org    sqlcgen.Organization
}

// headscaleClientFromContext returns the client and org attached by
// RequireHeadscaleClient.
func headscaleClientFromContext(r *http.Request) (client *hscl.Client, org sqlcgen.Organization, ok bool) {
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
// organization's headscale client is resolved. It answers 404 for an
// organization whose headscale never started, as nothing next could act on
// exists yet, and 409+notConfigured if headscale isn't running.
func (h *Handlers) RequireHeadscaleClient(next http.HandlerFunc) http.HandlerFunc {
	return h.withHeadscale(next, func(w http.ResponseWriter, r *http.Request, _ sqlcgen.Organization) {
		h.writeError(w, http.StatusNotFound, "not found")
	})
}

// ReadHeadscale is RequireHeadscaleClient, except that for an organization
// whose headscale never started it runs next with a nil client, so next
// answers with empty results.
func (h *Handlers) ReadHeadscale(next http.HandlerFunc) http.HandlerFunc {
	return h.withHeadscale(next, func(w http.ResponseWriter, r *http.Request, org sqlcgen.Organization) {
		next(w, withHeadscaleContext(r, nil, org))
	})
}

// ProvisionHeadscale is RequireHeadscaleClient for operations that need
// headscale: it starts the organization's headscale first if it never
// started, then runs next.
func (h *Handlers) ProvisionHeadscale(next http.HandlerFunc) http.HandlerFunc {
	return h.withHeadscale(next, func(w http.ResponseWriter, r *http.Request, org sqlcgen.Organization) {
		client, err := h.hs.Ensure(r.Context(), org.Subdomain)
		if err != nil {
			h.loggerFromContext(r.Context()).Warn("start headscale", "error", err)
			h.writeError(w, http.StatusServiceUnavailable, "headscale is unavailable right now, try again in a moment")
			return
		}
		next(w, withHeadscaleContext(r, client, org))
	})
}

// withHeadscale runs next with the caller's organization's headscale
// client in the request context. An organization whose headscale never
// started is handed to unprovisioned instead; one whose headscale isn't
// running gets 409+notConfigured.
func (h *Handlers) withHeadscale(next http.HandlerFunc, unprovisioned func(w http.ResponseWriter, r *http.Request, org sqlcgen.Organization)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client, org, err := h.resolveHeadscale(r)
		if org.Subdomain != "" {
			r = r.WithContext(h.withOrgLogger(r.Context(), org.Subdomain))
		}
		switch {
		case errors.Is(err, orgs.ErrNotProvisioned):
			unprovisioned(w, r, org)
		case errors.Is(err, errNoHeadscale):
			h.writeJSON(w, http.StatusConflict, map[string]bool{"notConfigured": true})
		case err != nil:
			h.serverError(w, r, err)
		default:
			next(w, withHeadscaleContext(r, client, org))
		}
	}
}

// withHeadscaleContext returns r carrying client and org for
// headscaleClientFromContext.
func withHeadscaleContext(r *http.Request, client *hscl.Client, org sqlcgen.Organization) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), headscaleClientContextKey, headscaleContext{client: client, org: org}))
}
