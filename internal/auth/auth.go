// Package auth wires up goth-based OAuth login (starting with Google) and the
// session/middleware plumbing used to protect handlers.
package auth

import (
	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/google"
)

// SessionUserKey is the session value holding the authenticated user's id.
const SessionUserKey = "user_id"

const SessionMaxAge = 30 * 24 * 60 * 60 // 30 days, in seconds

// Setup registers the configured OAuth providers with goth and points gothic
// at a cookie-backed session store keyed by sessionSecret. It must be called
// once at startup before any auth routes are served.
func Setup(sessionSecret, googleClientID, googleClientSecret, googleCallbackURL string) {
	store := sessions.NewCookieStore([]byte(sessionSecret))
	store.Options.HttpOnly = true
	store.Options.Path = "/"
	store.MaxAge(SessionMaxAge)
	gothic.Store = store

	goth.UseProviders(
		google.New(googleClientID, googleClientSecret, googleCallbackURL, "email", "profile"),
	)
}
