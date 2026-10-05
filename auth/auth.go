// Package auth wires up goth-based OAuth login and the session/middleware
// plumbing used to protect handlers.
package auth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/google"
	"github.com/markbates/goth/providers/microsoftonline"
	"github.com/markbates/goth/providers/openidConnect"
	"github.com/ownscalenet/ownscale/derrors"
)

// SessionUserKey is the session value holding the authenticated user's id.
const SessionUserKey = "user_id"

const SessionMaxAge = 30 * 24 * 60 * 60 // 30 days, in seconds

// UpstreamProviderName is the goth provider name.
// It's what appears in the browser-facing routes ("/auth/oidc", "/auth/oidc/callback")
const UpstreamProviderName = "oidc"
const GoogleProviderName = "google"
const MicrosoftProviderName = "microsoft"

// Setup configures gothic's cookie-backed session store, keyed by
// sessionSecret. It must be called once at startup, before any auth routes
// are served and before RegisterUpstreamOIDC.
func Setup(sessionSecret string) {
	store := sessions.NewCookieStore([]byte(sessionSecret))
	store.Options.HttpOnly = true
	store.Options.Secure = true
	store.Options.SameSite = http.SameSiteLaxMode
	store.Options.Path = "/"
	store.MaxAge(SessionMaxAge)
	gothic.Store = hostPrefixStore{store}
}

// hostPrefixStore names cookies with the __Host- prefix so sibling
// subdomains can't overwrite them with Domain=<base> cookies.
type hostPrefixStore struct {
	*sessions.CookieStore
}

func (s hostPrefixStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	return s.CookieStore.Get(r, "__Host-"+name)
}

func (s hostPrefixStore) New(r *http.Request, name string) (*sessions.Session, error) {
	return s.CookieStore.New(r, "__Host-"+name)
}

func RegisterUpstreamOIDC(issuer, clientID, clientSecret, callbackURL string) (err error) {
	defer derrors.Wrap(&err, "auth.RegisterUpstreamOIDC(%s)", clientID)
	discoveryURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	provider, err := openidConnect.New(clientID, clientSecret, callbackURL, discoveryURL, "openid", "email", "profile")
	if err != nil {
		return fmt.Errorf("build upstream OIDC provider %q: %w", issuer, err)
	}
	provider.SetName(UpstreamProviderName)

	goth.UseProviders(provider)
	return nil
}

func RegisterGoogle(clientID, clientSecret, callbackURL string) (err error) {
	defer derrors.Wrap(&err, "auth.RegisterGoogle(%s)", clientID)
	provider := google.New(clientID, clientSecret, callbackURL, "email", "profile")
	provider.SetName(GoogleProviderName)

	goth.UseProviders(provider)
	return nil
}

func RegisterMicrosoft(clientID, clientSecret, callbackURL string) (err error) {
	defer derrors.Wrap(&err, "auth.RegisterMicrosoft(%s)", clientID)
	provider := microsoftonline.New(clientID, clientSecret, callbackURL)
	provider.SetName(MicrosoftProviderName)

	goth.UseProviders(provider)
	return nil
}
