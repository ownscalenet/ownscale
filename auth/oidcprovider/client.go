package oidcprovider

import (
	"encoding/json"
	"net/url"
	"slices"
	"time"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

type client struct {
	row             sqlcgen.OidcClient
	idTokenLifetime time.Duration
	loginURL        func(string) string
}

func newClient(row sqlcgen.OidcClient, idTokenLifetime time.Duration, loginURL func(string) string) *client {
	return &client{row: row, idTokenLifetime: idTokenLifetime, loginURL: loginURL}
}

func (c *client) GetID() string { return c.row.ClientID }

func (c *client) RedirectURIs() []string {
	var uris []string
	if err := json.Unmarshal([]byte(c.row.RedirectUris), &uris); err != nil {
		return nil
	}
	return uris
}

// PostLogoutRedirectURIs returns per-redirect-uri site roots. This gives
// downstream web apps a conservative first-party landing page after RP-initiated
// logout without needing an extra per-client config surface in ownscale.
func (c *client) PostLogoutRedirectURIs() []string {
	roots := make([]string, 0, len(c.RedirectURIs()))
	seen := make(map[string]bool)
	for _, raw := range c.RedirectURIs() {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}
		u.Path = "/"
		u.RawPath = ""
		u.RawQuery = ""
		u.Fragment = ""
		root := u.String()
		if !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	return roots
}

func (c *client) ApplicationType() op.ApplicationType { return op.ApplicationTypeWeb }
func (c *client) AuthMethod() oidc.AuthMethod         { return oidc.AuthMethodBasic }
func (c *client) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}
func (c *client) GrantTypes() []oidc.GrantType {
	return []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken}
}
func (c *client) LoginURL(id string) string            { return c.loginURL(id) }
func (c *client) AccessTokenType() op.AccessTokenType  { return op.AccessTokenTypeBearer }
func (c *client) IDTokenLifetime() time.Duration       { return c.idTokenLifetime }
func (c *client) DevMode() bool                        { return false }
func (c *client) IDTokenUserinfoClaimsAssertion() bool { return true }
func (c *client) ClockSkew() time.Duration             { return 0 }
func (c *client) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return func(scopes []string) []string { return scopes }
}
func (c *client) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return func(scopes []string) []string { return scopes }
}

func (c *client) IsScopeAllowed(scope string) bool {
	return slices.Contains([]string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess}, scope)
}
