// Package auth wires up goth-based OAuth login and the session/middleware
// plumbing used to protect handlers.
package auth

import (
	"fmt"
	"strings"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/openidConnect"
)

// SessionUserKey is the session value holding the authenticated user's id.
const SessionUserKey = "user_id"

const SessionMaxAge = 30 * 24 * 60 * 60 // 30 days, in seconds

// UpstreamProviderName is the goth provider name.
// It's what appears in the browser-facing routes ("/auth/oidc", "/auth/oidc/callback")
const UpstreamProviderName = "oidc"

// Setup configures gothic's cookie-backed session store, keyed by
// sessionSecret. It must be called once at startup, before any auth routes
// are served and before RegisterUpstreamOIDC.
func Setup(sessionSecret string) {
	store := sessions.NewCookieStore([]byte(sessionSecret))
	store.Options.HttpOnly = true
	store.Options.Path = "/"
	store.MaxAge(SessionMaxAge)
	gothic.Store = store
}

func RegisterUpstreamOIDC(issuer, clientID, clientSecret, callbackURL string) (err error) {
	discoveryURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	provider, err := openidConnect.New(clientID, clientSecret, callbackURL, discoveryURL, "openid", "email", "profile")
	if err != nil {
		return fmt.Errorf("build upstream OIDC provider %q: %w", issuer, err)
	}
	provider.SetName(UpstreamProviderName)

	goth.UseProviders(provider)
	return nil
}
