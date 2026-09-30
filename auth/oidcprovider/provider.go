package oidcprovider

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/markbates/goth/gothic"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// New constructs ownscale's OpenID Provider and its backing storage.
func New(cfg *config.Config, queries *sqlcgen.Queries) (_ op.OpenIDProvider, _ *Storage, err error) {
	defer derrors.Wrap(&err, "oidcprovider.New(cfg, queries)")

	accessTokenLifetime, err := time.ParseDuration(cfg.Auth.OIDCProvider.AccessTokenLifetime)
	if err != nil {
		return nil, nil, err
	}
	idTokenLifetime, err := time.ParseDuration(cfg.Auth.OIDCProvider.IDTokenLifetime)
	if err != nil {
		return nil, nil, err
	}
	rsaKey, err := LoadOrGenerateSigningKey(cfg.Auth.OIDCProvider.SigningKeyPath)
	if err != nil {
		return nil, nil, err
	}
	storage := newStorage(queries, newSigningKey(rsaKey), cfg.Auth.OIDCProvider.Issuer, accessTokenLifetime, idTokenLifetime)
	cryptoKey := sha256.Sum256([]byte(cfg.Auth.SessionSecret + ":oidc-crypto-key"))
	provider, err := op.NewProvider(&op.Config{
		CryptoKey:                cryptoKey,
		DefaultLogoutRedirectURI: "/",
		CodeMethodS256:           true,
		AuthMethodPost:           true,
		AuthMethodPrivateKeyJWT:  false,
		GrantTypeRefreshToken:    true,
		RequestObjectSupported:   false,
	}, storage, op.StaticIssuer(cfg.Auth.OIDCProvider.Issuer))
	if err != nil {
		return nil, nil, err
	}
	storage.SetLoginURL(newLoginURL(storage, op.AuthCallbackURL(provider)))
	return newAuthorizingProvider(provider, storage), storage, nil
}

// newLoginURL returns where /authorize sends the browser for auth request id.
// A request already authorized from the session goes straight to its
// callback; any other one goes to the login page, which then lets a signed-in
// user switch accounts instead of looping back to a request that isn't done.
func newLoginURL(s *Storage, callbackURL func(context.Context, string) string) func(string) string {
	return func(id string) string {
		next := callbackURL(context.Background(), id)
		if s.authRequestDone(id) {
			return next
		}
		return "/login?next=" + url.QueryEscape(next)
	}
}

type sessionUserKey struct{}

type authorizingProvider struct {
	op.OpenIDProvider
	storage        *Storage
	authorizeRoute http.Handler
}

func newAuthorizingProvider(provider op.OpenIDProvider, storage *Storage) op.OpenIDProvider {
	wrapper := &authorizingProvider{OpenIDProvider: provider, storage: storage}
	var authorizeHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op.Authorize(w, r, wrapper)
	})
	authorizeHandler = op.NewIssuerInterceptor(provider.IssuerFromRequest).Handler(authorizeHandler)
	authorizeHandler = sessionUserInterceptor(authorizeHandler)
	wrapper.authorizeRoute = authorizeHandler
	return wrapper
}

func (p *authorizingProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == p.AuthorizationEndpoint().Relative() {
		p.authorizeRoute.ServeHTTP(w, r)
		return
	}
	p.OpenIDProvider.ServeHTTP(w, r)
}

func (p *authorizingProvider) HttpHandler() http.Handler { return p }

func (p *authorizingProvider) ValidateAuthRequest(ctx context.Context, authReq *oidc.AuthRequest, storage op.Storage, verifier *op.IDTokenHintVerifier) (string, error) {
	client, err := storage.GetClientByClientID(ctx, authReq.ClientID)
	if err != nil {
		return "", oidc.ErrInvalidRequestRedirectURI().WithDescription("unable to retrieve client by id").WithParent(err)
	}
	subject, err := op.ValidateAuthRequestClient(ctx, authReq, client, verifier)
	if err != nil {
		return "", err
	}
	if slices.Contains(authReq.Prompt, oidc.PromptLogin) {
		return subject, nil
	}
	if sessionUserID, _ := ctx.Value(sessionUserKey{}).(string); sessionUserID != "" {
		// A non-member is sent to the login page to pick another account.
		if err := p.storage.checkMember(ctx, authReq.ClientID, sessionUserID); errors.Is(err, ErrNotMember) {
			return subject, nil
		} else if err != nil {
			return "", oidc.ErrServerError().WithParent(err)
		}
		return sessionUserID, nil
	}
	return subject, nil
}

func sessionUserInterceptor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := gothic.Store.Get(r, gothic.SessionName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		userID, _ := sess.Values["user_id"].(string)
		if userID == "" {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionUserKey{}, userID)))
	})
}

// AuthRequestIDFromNext extracts the OIDC auth request id from a relative
// authorize callback URL previously created with op.AuthCallbackURL.
func AuthRequestIDFromNext(next string) string {
	u, err := url.Parse(next)
	if err != nil || u.Path != "/authorize/callback" {
		return ""
	}
	return u.Query().Get("id")
}
