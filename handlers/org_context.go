package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
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
func headscaleClientFromContext(r *http.Request) (*hscl.Client, sqlcgen.Organization) {
	c, _ := r.Context().Value(headscaleClientContextKey).(headscaleContext)
	return c.client, c.org
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

// RequireHeadscaleClient wraps next so it only runs with the caller's
// organization's running headscale client, starting headscale first if it's
// asleep. If headscale can't run, it answers 503.
func (h *Handlers) RequireHeadscaleClient(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok {
			h.serverError(w, r, errors.New("RequireHeadscaleClient needs an authenticated user"))
			return
		}
		org, err := h.queries.GetOrganization(r.Context(), user.OrganizationID)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		r = r.WithContext(h.withOrgLogger(r.Context(), org.Subdomain))
		client, err := h.hs.Ensure(r.Context(), org.Subdomain)
		if err != nil {
			h.loggerFromContext(r.Context()).Warn("get headscale client", "error", err)
			h.writeError(w, http.StatusServiceUnavailable, "headscale is unavailable right now, try again in a moment")
			return
		}
		next(w, withHeadscaleContext(r, client, org))
	}
}

// withHeadscaleContext returns r carrying client and org for
// headscaleClientFromContext.
func withHeadscaleContext(r *http.Request, client *hscl.Client, org sqlcgen.Organization) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), headscaleClientContextKey, headscaleContext{client: client, org: org}))
}
