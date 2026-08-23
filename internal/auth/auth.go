// Package auth wires up goth-based OAuth login and the session/middleware
// plumbing used to protect handlers.
package auth

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

// RegisterUpstreamOIDC registers ownscale's upstream OIDC client (see
// config.UpstreamOIDCConfig) with goth under UpstreamProviderName. It
// performs OIDC discovery (".well-known/openid-configuration") against
// issuer itself -- rather than via openidConnect.New, which does the same
// thing internally but always over http.DefaultClient, too early for us to
// inject extraCACertPath's trust below -- and returns an error if that
// fails, so callers should treat this as a startup-time dependency check,
// not a lazy/best-effort registration.
//
// extraCACertPath, if non-empty and present on disk, is an additional CA
// certificate (PEM-encoded) trusted for discovery and all subsequent
// requests to issuer, on top of the system trust store. This is for
// ownscale to trust Caddy's self-signed local CA in a local-dev env.
func RegisterUpstreamOIDC(issuer, clientID, clientSecret, callbackURL, extraCACertPath string) (err error) {
	httpClient, err := httpClientTrusting(extraCACertPath)
	if err != nil {
		return fmt.Errorf("build upstream OIDC HTTP client: %w", err)
	}

	discovered, err := discoverOpenIDConfig(goth.HTTPClientWithFallBack(httpClient), issuer)
	if err != nil {
		return fmt.Errorf("discover upstream OIDC provider %q: %w", issuer, err)
	}

	provider, err := openidConnect.NewCustomisedURL(
		clientID, clientSecret, callbackURL,
		discovered.AuthEndpoint, discovered.TokenEndpoint, discovered.Issuer,
		discovered.UserInfoEndpoint, discovered.EndSessionEndpoint,
		"openid", "email", "profile",
	)
	if err != nil {
		return fmt.Errorf("build upstream OIDC provider %q: %w", issuer, err)
	}
	provider.HTTPClient = httpClient
	provider.SetName(UpstreamProviderName)

	goth.UseProviders(provider)
	return nil
}

// discoverOpenIDConfig fetches and parses issuer's OIDC discovery document
// via httpClient, matching openidConnect.OpenIDConfig's JSON shape so the
// result can be fed straight into openidConnect.NewCustomisedURL.
func discoverOpenIDConfig(httpClient *http.Client, issuer string) (*openidConnect.OpenIDConfig, error) {
	discoveryURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"

	res, err := httpClient.Get(discoveryURL)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("non-success status %d from %s", res.StatusCode, discoveryURL)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var cfg openidConnect.OpenIDConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("parse discovery document from %s: %w", discoveryURL, err)
	}
	return &cfg, nil
}

// httpClientTrusting returns an *http.Client trusting the system CA pool
// plus, if extraCACertPath names a file that exists, the PEM certificate(s)
// in it. A nil *http.Client (meaning "use net/http's default behavior") is
// returned when extraCACertPath is empty or the file doesn't exist, so this
// is a no-op in production.
func httpClientTrusting(extraCACertPath string) (*http.Client, error) {
	if extraCACertPath == "" {
		return nil, nil
	}
	pemBytes, err := os.ReadFile(extraCACertPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read extra CA cert %q: %w", extraCACertPath, err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("no certificates found in %q", extraCACertPath)
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}, nil
}
