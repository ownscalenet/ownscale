// Package server wires together the HTTP route table: the JSON /api/*
// surface, the browser-driven OAuth redirect routes, and the SPA catch-all.
// It is factored out of main() so integration tests can build the exact
// same mux without spawning the real process.
package server

import (
	"net/http"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/handlers"
)

// NewMux builds the admin panel's http.ServeMux. static serves the built
// frontend (SPA fallback to index.html for client-side routes); pass
// http.NotFoundHandler() if the frontend hasn't been built. Every request is
// logged via h.Logger() before being dispatched.
func NewMux(h *handlers.Handlers, static http.Handler) http.Handler {
	requireAuthAPI := func(next http.HandlerFunc) http.HandlerFunc {
		return auth.RequireAuthAPI(h.Queries(), next)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/session", requireAuthAPI(h.Session))
	mux.HandleFunc("POST /api/logout", requireAuthAPI(h.APILogout))

	mux.HandleFunc("POST /api/organizations", requireAuthAPI(h.CreateOrganization))

	// Caddy's on-demand TLS "ask" endpoint; unauthenticated (checked by
	// TLSAsk itself against known organizations/subdomains).
	mux.HandleFunc("GET /api/tls-ask", h.TLSAsk)

	// Browser-driven OAuth flow: real redirects, not JSON.
	mux.HandleFunc("GET /auth/{provider}/callback", h.AuthCallback)
	mux.HandleFunc("GET /auth/{provider}", h.BeginAuth)
	mux.HandleFunc("GET /logout", h.Logout)

	// SPA: serves static assets and falls back to index.html for
	// client-side routes (/, /login, /machines, ...).
	mux.Handle("/", static)

	return loggingMiddleware(h.Logger(), mux)
}
