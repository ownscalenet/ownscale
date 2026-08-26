package oidcprovider

import (
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

const authRequestLifetime = 15 * time.Minute
const refreshTokenLifetime = 24 * time.Hour

type authRequest struct {
	ID            string
	CreationDate  time.Time
	ApplicationID string
	CallbackURI   string
	TransferState string
	Prompt        []string
	UserID        string
	Scopes        []string
	ResponseType  oidc.ResponseType
	ResponseMode  oidc.ResponseMode
	Nonce         string
	CodeChallenge *oidc.CodeChallenge
	done          bool
	authTime      time.Time
}

func newAuthRequest(authReq *oidc.AuthRequest, userID string) *authRequest {
	req := &authRequest{
		CreationDate:  time.Now(),
		ApplicationID: authReq.ClientID,
		CallbackURI:   authReq.RedirectURI,
		TransferState: authReq.State,
		Prompt:        append([]string(nil), authReq.Prompt...),
		UserID:        userID,
		Scopes:        append([]string(nil), authReq.Scopes...),
		ResponseType:  authReq.ResponseType,
		ResponseMode:  authReq.ResponseMode,
		Nonce:         authReq.Nonce,
	}
	if authReq.CodeChallenge != "" {
		req.CodeChallenge = &oidc.CodeChallenge{Challenge: authReq.CodeChallenge, Method: authReq.CodeChallengeMethod}
	}
	if userID != "" {
		req.done = true
		req.authTime = time.Now()
	}
	return req
}

func (a *authRequest) GetID() string  { return a.ID }
func (a *authRequest) GetACR() string { return "" }
func (a *authRequest) GetAMR() []string {
	if a.done {
		// Non-standard value: the user authenticated via ownscale's
		// upstream OIDC login (see internal/auth), whatever the actual
		// upstream IdP (Google, Dex, ...) is configured to be.
		return []string{"oidc"}
	}
	return nil
}
func (a *authRequest) GetAudience() []string                 { return []string{a.ApplicationID} }
func (a *authRequest) GetAuthTime() time.Time                { return a.authTime }
func (a *authRequest) GetClientID() string                   { return a.ApplicationID }
func (a *authRequest) GetCodeChallenge() *oidc.CodeChallenge { return a.CodeChallenge }
func (a *authRequest) GetNonce() string                      { return a.Nonce }
func (a *authRequest) GetRedirectURI() string                { return a.CallbackURI }
func (a *authRequest) GetResponseType() oidc.ResponseType    { return a.ResponseType }
func (a *authRequest) GetResponseMode() oidc.ResponseMode    { return a.ResponseMode }
func (a *authRequest) GetScopes() []string                   { return a.Scopes }
func (a *authRequest) GetState() string                      { return a.TransferState }
func (a *authRequest) GetSubject() string                    { return a.UserID }
func (a *authRequest) Done() bool                            { return a.done }

type token struct {
	ID             string
	ApplicationID  string
	Subject        string
	RefreshTokenID string
	Audience       []string
	Expiration     time.Time
	Scopes         []string
}

type refreshToken struct {
	ID            string
	Token         string
	AuthTime      time.Time
	AMR           []string
	Audience      []string
	UserID        string
	ApplicationID string
	Expiration    time.Time
	Scopes        []string
	AccessToken   string
}

type refreshTokenRequest struct{ *refreshToken }

func (r *refreshTokenRequest) GetAMR() []string                 { return r.AMR }
func (r *refreshTokenRequest) GetAudience() []string            { return r.Audience }
func (r *refreshTokenRequest) GetAuthTime() time.Time           { return r.AuthTime }
func (r *refreshTokenRequest) GetClientID() string              { return r.ApplicationID }
func (r *refreshTokenRequest) GetScopes() []string              { return r.Scopes }
func (r *refreshTokenRequest) GetSubject() string               { return r.UserID }
func (r *refreshTokenRequest) SetCurrentScopes(scopes []string) { r.Scopes = scopes }

func refreshTokenRequestFromBusiness(token *refreshToken) op.RefreshTokenRequest {
	return &refreshTokenRequest{refreshToken: token}
}
