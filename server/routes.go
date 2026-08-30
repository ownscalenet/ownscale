// Package server wires together the HTTP route table: the JSON /api/*
// surface, the browser-driven OAuth redirect routes, and the SPA catch-all.
// It is factored out of main() so integration tests can build the exact
// same mux without spawning the real process.
package server

import (
	"log/slog"
	"net/http"

	"github.com/ownscalenet/ownscale/handlers"
)

// NewMux builds the admin panel's http.ServeMux. requireAuthAPI wraps an
// http.HandlerFunc so it only runs for an authenticated session (see
// auth.RequireAuthAPI), responding 401 JSON otherwise. static serves the
// built frontend (SPA fallback to index.html for client-side routes); pass
// http.NotFoundHandler() if the frontend hasn't been built. Every request is
// logged via logger before being dispatched.
func NewMux(h *handlers.Handlers, requireAuthAPI func(http.HandlerFunc) http.HandlerFunc, logger *slog.Logger, static http.Handler, oidcHandler http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/session", requireAuthAPI(h.Session))
	mux.HandleFunc("POST /api/logout", requireAuthAPI(h.APILogout))

	mux.HandleFunc("POST /api/organizations", requireAuthAPI(h.CreateOrganization))

	mux.HandleFunc("GET /api/machines", requireAuthAPI(h.RequireMachinesClient(h.ListMachines)))
	mux.HandleFunc("POST /api/machines/register", requireAuthAPI(h.RequireMachinesClient(h.RegisterMachine)))
	mux.HandleFunc("POST /api/machines/{id}/rename", requireAuthAPI(h.RequireMachinesClient(h.RenameMachine)))
	mux.HandleFunc("POST /api/machines/{id}/tags", requireAuthAPI(h.RequireMachinesClient(h.SetMachineTags)))
	mux.HandleFunc("POST /api/machines/{id}/routes", requireAuthAPI(h.RequireMachinesClient(h.SetMachineRoutes)))
	mux.HandleFunc("POST /api/machines/{id}/expire", requireAuthAPI(h.RequireMachinesClient(h.ExpireMachine)))
	mux.HandleFunc("DELETE /api/machines/{id}", requireAuthAPI(h.RequireMachinesClient(h.DeleteMachine)))

	mux.HandleFunc("GET /api/auth-keys", requireAuthAPI(h.RequireMachinesClient(h.ListAuthKeys)))
	mux.HandleFunc("POST /api/auth-keys", requireAuthAPI(h.RequireMachinesClient(h.CreateAuthKey)))
	mux.HandleFunc("POST /api/auth-keys/{id}/expire", requireAuthAPI(h.RequireMachinesClient(h.ExpireAuthKey)))

	mux.HandleFunc("GET /api/members", requireAuthAPI(h.ListMembers))
	mux.HandleFunc("POST /api/members/transfer-ownership", requireAuthAPI(h.TransferOwnership))
	mux.HandleFunc("POST /api/members/{id}/role", requireAuthAPI(h.UpdateMemberRole))
	mux.HandleFunc("DELETE /api/members/{id}", requireAuthAPI(h.RemoveMember))

	mux.HandleFunc("GET /api/invites", requireAuthAPI(h.ListInvites))
	mux.HandleFunc("POST /api/invites", requireAuthAPI(h.CreateInvite))
	mux.HandleFunc("DELETE /api/invites/{id}", requireAuthAPI(h.DeleteInvite))
	mux.HandleFunc("GET /api/invites/{token}", h.GetInvitePreview) // public: preview before login
	mux.HandleFunc("POST /api/invites/{token}/accept", requireAuthAPI(h.AcceptInvite))

	mux.HandleFunc("GET /api/access-control", requireAuthAPI(h.RequireActiveOrg(h.GetAccessControlPolicy)))
	mux.HandleFunc("PUT /api/access-control", requireAuthAPI(h.RequireActiveOrg(h.UpdateAccessControlPolicy)))
	mux.HandleFunc("POST /api/access-control/format", requireAuthAPI(h.FormatAccessControlPolicy))

	mux.HandleFunc("GET /api/auth-restrictions", requireAuthAPI(h.RequireActiveOrg(h.GetAuthRestrictions)))
	mux.HandleFunc("PUT /api/auth-restrictions", requireAuthAPI(h.RequireActiveOrg(h.UpdateAuthRestrictions)))

	// Caddy's on-demand TLS "ask" endpoint; unauthenticated (checked by
	// TLSAsk itself against known organizations/subdomains).
	mux.HandleFunc("GET /api/tls-ask", h.TLSAsk)

	// Browser-driven OAuth flow: real redirects, not JSON.
	mux.HandleFunc("GET /auth/{provider}/callback", h.AuthCallback)
	mux.HandleFunc("GET /auth/{provider}", h.BeginAuth)
	mux.HandleFunc("GET /logout", h.Logout)

	if oidcHandler != nil {
		for _, path := range []string{
			"/.well-known/openid-configuration",
			"/authorize",
			"/authorize/callback",
			"/oauth/token",
			"/oauth/introspect",
			"/userinfo",
			"/revoke",
			"/end_session",
			"/keys",
			"/device_authorization",
			"/healthz",
			"/ready",
		} {
			mux.Handle(path, oidcHandler)
		}
	}

	// SPA: serves static assets and falls back to index.html for
	// client-side routes (/, /login, /machines, ...).
	mux.Handle("/", static)

	return loggingMiddleware(logger, securityHeadersMiddleware(mux))
}
