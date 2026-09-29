package oidcprovider

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/rs/xid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// Storage implements zitadel/oidc's storage interfaces using ownscale's real
// users and oidc_clients tables plus in-memory short-lived OAuth artifacts.
type Storage struct {
	queries             *sqlcgen.Queries
	lock                sync.Mutex
	authRequests        map[string]*authRequest
	codes               map[string]authCode
	tokens              map[string]*token
	refreshTokens       map[string]*refreshToken
	signingKey          *signingKey
	accessTokenLifetime time.Duration
	idTokenLifetime     time.Duration
	issuer              string
	loginURL            func(string) string
}

type authCode struct {
	requestID string
	expiresAt time.Time
}

var _ op.Storage = (*Storage)(nil)
var _ op.CanSetUserinfoFromRequest = (*Storage)(nil)

func newStorage(queries *sqlcgen.Queries, signingKey *signingKey, issuer string, accessTokenLifetime, idTokenLifetime time.Duration) *Storage {
	s := &Storage{
		queries:             queries,
		authRequests:        make(map[string]*authRequest),
		codes:               make(map[string]authCode),
		tokens:              make(map[string]*token),
		refreshTokens:       make(map[string]*refreshToken),
		signingKey:          signingKey,
		accessTokenLifetime: accessTokenLifetime,
		idTokenLifetime:     idTokenLifetime,
		issuer:              issuer,
		loginURL:            func(id string) string { return "/login?next=" + id },
	}
	go s.cleanupLoop()
	return s
}

// Issuer returns the configured OIDC issuer URL.
func (s *Storage) Issuer() string {
	return s.issuer
}

// SetLoginURL configures the browser-facing login URL used during /authorize.
func (s *Storage) SetLoginURL(loginURL func(string) string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.loginURL = loginURL
}

// ErrNotMember is returned when a user tries to sign in to the headscale of
// an organization they don't belong to.
var ErrNotMember = errors.New("user is not a member of the client's organization")

// AuthorizeAuthRequest marks a pending auth request as completed for userID.
// It returns ErrNotMember if userID doesn't belong to the organization that
// owns the request's client.
func (s *Storage) AuthorizeAuthRequest(ctx context.Context, id, userID string) (err error) {
	defer derrors.Wrap(&err, "s.AuthorizeAuthRequest(%q, %q)", id, userID)

	s.lock.Lock()
	request, ok := s.authRequests[id]
	s.lock.Unlock()
	if !ok {
		return fmt.Errorf("auth request not found")
	}
	if err := s.checkMember(ctx, request.ApplicationID, userID); err != nil {
		return err
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	request.UserID = userID
	request.done = true
	request.authTime = time.Now()
	return nil
}

func (s *Storage) checkMember(ctx context.Context, clientID, userID string) error {
	client, err := s.queries.GetOIDCClientByClientID(ctx, clientID)
	if err != nil {
		return err
	}
	user, err := s.queries.GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotMember
	}
	if err != nil {
		return err
	}
	if user.OrganizationID != client.OrganizationID {
		return ErrNotMember
	}
	return nil
}

func (s *Storage) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.cleanupExpired(time.Now())
	}
}

func (s *Storage) cleanupExpired(now time.Time) {
	s.lock.Lock()
	defer s.lock.Unlock()
	for id, req := range s.authRequests {
		if req.CreationDate.Add(authRequestLifetime).Before(now) {
			delete(s.authRequests, id)
		}
	}
	for code, entry := range s.codes {
		if entry.expiresAt.Before(now) {
			delete(s.codes, code)
		}
	}
	for id, tok := range s.tokens {
		if tok.Expiration.Before(now) {
			delete(s.tokens, id)
		}
	}
	for id, tok := range s.refreshTokens {
		if tok.Expiration.Before(now) {
			delete(s.refreshTokens, id)
		}
	}
}

func (s *Storage) CreateAuthRequest(ctx context.Context, authReq *oidc.AuthRequest, userID string) (_ op.AuthRequest, err error) {
	if len(authReq.Prompt) == 1 && authReq.Prompt[0] == oidc.PromptNone && userID == "" {
		return nil, oidc.ErrLoginRequired()
	}

	req := newAuthRequest(authReq, userID)
	req.ID = xid.New().String()

	s.lock.Lock()
	defer s.lock.Unlock()
	s.authRequests[req.ID] = req
	return req, nil
}

func (s *Storage) AuthRequestByID(ctx context.Context, id string) (_ op.AuthRequest, err error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	req, ok := s.authRequests[id]
	if !ok {
		return nil, fmt.Errorf("auth request not found")
	}
	return req, nil
}

func (s *Storage) AuthRequestByCode(ctx context.Context, code string) (_ op.AuthRequest, err error) {
	s.lock.Lock()
	entry, ok := s.codes[code]
	s.lock.Unlock()
	if !ok || entry.expiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("code invalid or expired")
	}
	return s.AuthRequestByID(ctx, entry.requestID)
}

func (s *Storage) SaveAuthCode(ctx context.Context, id string, code string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.codes[code] = authCode{requestID: id, expiresAt: time.Now().Add(authRequestLifetime)}
	return nil
}

func (s *Storage) DeleteAuthRequest(ctx context.Context, id string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.authRequests, id)
	for code, entry := range s.codes {
		if entry.requestID == id {
			delete(s.codes, code)
		}
	}
	return nil
}

func (s *Storage) CreateAccessToken(ctx context.Context, request op.TokenRequest) (string, time.Time, error) {
	applicationID, _, _ := getTokenInfoFromRequest(request)
	tok := &token{
		ID:            xid.New().String(),
		ApplicationID: applicationID,
		Subject:       request.GetSubject(),
		Audience:      append([]string(nil), request.GetAudience()...),
		Expiration:    time.Now().Add(s.accessTokenLifetime),
		Scopes:        append([]string(nil), request.GetScopes()...),
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	s.tokens[tok.ID] = tok
	return tok.ID, tok.Expiration, nil
}

func (s *Storage) CreateAccessAndRefreshTokens(ctx context.Context, request op.TokenRequest, currentRefreshToken string) (accessTokenID string, newRefreshToken string, expiration time.Time, err error) {
	applicationID, authTime, amr := getTokenInfoFromRequest(request)
	newRefreshToken = xid.New().String()
	accessTokenID = xid.New().String()
	expiration = time.Now().Add(s.accessTokenLifetime)
	accessToken := &token{
		ID:             accessTokenID,
		ApplicationID:  applicationID,
		RefreshTokenID: newRefreshToken,
		Subject:        request.GetSubject(),
		Audience:       append([]string(nil), request.GetAudience()...),
		Expiration:     expiration,
		Scopes:         append([]string(nil), request.GetScopes()...),
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	if currentRefreshToken != "" {
		current, ok := s.refreshTokens[currentRefreshToken]
		if !ok {
			return "", "", time.Time{}, fmt.Errorf("invalid refresh token")
		}
		delete(s.refreshTokens, currentRefreshToken)
		delete(s.tokens, current.AccessToken)
		if current.Expiration.Before(time.Now()) {
			return "", "", time.Time{}, fmt.Errorf("expired refresh token")
		}
	}

	refresh := &refreshToken{
		ID:            newRefreshToken,
		Token:         newRefreshToken,
		AuthTime:      authTime,
		AMR:           append([]string(nil), amr...),
		Audience:      append([]string(nil), request.GetAudience()...),
		UserID:        request.GetSubject(),
		ApplicationID: applicationID,
		Expiration:    time.Now().Add(refreshTokenLifetime),
		Scopes:        append([]string(nil), request.GetScopes()...),
		AccessToken:   accessTokenID,
	}
	s.tokens[accessTokenID] = accessToken
	s.refreshTokens[newRefreshToken] = refresh
	return accessTokenID, newRefreshToken, expiration, nil
}

func (s *Storage) TokenRequestByRefreshToken(ctx context.Context, refreshToken string) (_ op.RefreshTokenRequest, err error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	tok, ok := s.refreshTokens[refreshToken]
	if !ok {
		return nil, fmt.Errorf("invalid refresh_token")
	}
	if tok.Expiration.Before(time.Now()) {
		delete(s.refreshTokens, refreshToken)
		return nil, fmt.Errorf("expired refresh_token")
	}
	return refreshTokenRequestFromBusiness(tok), nil
}

func (s *Storage) TerminateSession(ctx context.Context, userID string, clientID string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	for id, tok := range s.tokens {
		if tok.Subject == userID && tok.ApplicationID == clientID {
			delete(s.tokens, id)
			if tok.RefreshTokenID != "" {
				delete(s.refreshTokens, tok.RefreshTokenID)
			}
		}
	}
	return nil
}

func (s *Storage) RevokeToken(ctx context.Context, tokenOrID string, userID string, clientID string) *oidc.Error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if accessToken, ok := s.tokens[tokenOrID]; ok {
		if accessToken.ApplicationID != clientID {
			return oidc.ErrInvalidClient().WithDescription("token was not issued for this client")
		}
		delete(s.tokens, tokenOrID)
		return nil
	}
	refreshToken, ok := s.refreshTokens[tokenOrID]
	if !ok {
		return nil
	}
	if refreshToken.ApplicationID != clientID {
		return oidc.ErrInvalidClient().WithDescription("token was not issued for this client")
	}
	delete(s.refreshTokens, tokenOrID)
	delete(s.tokens, refreshToken.AccessToken)
	return nil
}

func (s *Storage) GetRefreshTokenInfo(ctx context.Context, clientID string, token string) (userID string, tokenID string, err error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	refreshToken, ok := s.refreshTokens[token]
	if !ok {
		return "", "", op.ErrInvalidRefreshToken
	}
	if refreshToken.ApplicationID != clientID {
		return "", "", op.ErrInvalidRefreshToken
	}
	return refreshToken.UserID, refreshToken.ID, nil
}

func (s *Storage) SigningKey(ctx context.Context) (op.SigningKey, error) { return s.signingKey, nil }
func (s *Storage) SignatureAlgorithms(ctx context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{s.signingKey.SignatureAlgorithm()}, nil
}
func (s *Storage) KeySet(ctx context.Context) ([]op.Key, error) {
	return []op.Key{&publicKey{signingKey: s.signingKey}}, nil
}

func (s *Storage) GetClientByClientID(ctx context.Context, clientID string) (_ op.Client, err error) {
	row, err := s.queries.GetOIDCClientByClientID(ctx, clientID)
	if err != nil {
		return nil, err
	}
	s.lock.Lock()
	loginURL := s.loginURL
	s.lock.Unlock()
	return newClient(row, s.idTokenLifetime, loginURL), nil
}

func (s *Storage) AuthorizeClientIDSecret(ctx context.Context, clientID, clientSecret string) error {
	row, err := s.queries.GetOIDCClientByClientID(ctx, clientID)
	if err != nil {
		return err
	}
	return bcrypt.CompareHashAndPassword([]byte(row.ClientSecretHash), []byte(clientSecret))
}

func (s *Storage) SetUserinfoFromScopes(ctx context.Context, userinfo *oidc.UserInfo, userID, clientID string, scopes []string) error {
	return nil
}

func (s *Storage) SetUserinfoFromRequest(ctx context.Context, userinfo *oidc.UserInfo, request op.IDTokenRequest, scopes []string) error {
	return s.setUserinfo(ctx, userinfo, request.GetSubject(), request.GetClientID(), scopes)
}

func (s *Storage) SetUserinfoFromToken(ctx context.Context, userinfo *oidc.UserInfo, tokenID, subject, origin string) error {
	s.lock.Lock()
	tok, ok := s.tokens[tokenID]
	s.lock.Unlock()
	if !ok {
		return fmt.Errorf("token is invalid or has expired")
	}
	if tok.Expiration.Before(time.Now()) {
		return fmt.Errorf("token is expired")
	}
	return s.setUserinfo(ctx, userinfo, tok.Subject, tok.ApplicationID, tok.Scopes)
}

func (s *Storage) SetIntrospectionFromToken(ctx context.Context, introspection *oidc.IntrospectionResponse, tokenID, subject, clientID string) error {
	s.lock.Lock()
	tok, ok := s.tokens[tokenID]
	s.lock.Unlock()
	if !ok {
		return fmt.Errorf("token is invalid")
	}
	introspection.Expiration = oidc.FromTime(tok.Expiration)
	if tok.Expiration.Before(time.Now()) {
		return fmt.Errorf("token is expired")
	}
	for _, audience := range tok.Audience {
		if audience != clientID {
			continue
		}
		userInfo := new(oidc.UserInfo)
		if err := s.setUserinfo(ctx, userInfo, tok.Subject, clientID, tok.Scopes); err != nil {
			return err
		}
		introspection.SetUserInfo(userInfo)
		introspection.Scope = tok.Scopes
		introspection.ClientID = tok.ApplicationID
		return nil
	}
	return fmt.Errorf("token is not valid for this client")
}

func (s *Storage) GetPrivateClaimsFromScopes(ctx context.Context, userID, clientID string, scopes []string) (map[string]any, error) {
	return nil, nil
}

func (s *Storage) GetKeyByIDAndClientID(ctx context.Context, keyID, clientID string) (*jose.JSONWebKey, error) {
	return nil, fmt.Errorf("jwt profile keys are not configured")
}

func (s *Storage) ValidateJWTProfileScopes(ctx context.Context, userID string, scopes []string) ([]string, error) {
	return nil, fmt.Errorf("jwt profile grant is not supported")
}

func (s *Storage) Health(ctx context.Context) error { return nil }

func (s *Storage) setUserinfo(ctx context.Context, userinfo *oidc.UserInfo, userID, clientID string, scopes []string) error {
	user, err := s.queries.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("user not found")
		}
		return err
	}
	for _, scope := range scopes {
		switch scope {
		case oidc.ScopeOpenID:
			userinfo.Subject = user.ID
		case oidc.ScopeEmail:
			userinfo.Email = user.Email
			userinfo.EmailVerified = oidc.Bool(user.EmailVerified != 0)
		case oidc.ScopeProfile:
			userinfo.Name = user.Name
			userinfo.Picture = user.AvatarUrl
		}
	}
	return nil
}

func getTokenInfoFromRequest(request op.TokenRequest) (clientID string, authTime time.Time, amr []string) {
	if authReq, ok := request.(*authRequest); ok {
		return authReq.ApplicationID, authReq.authTime, authReq.GetAMR()
	}
	if refreshReq, ok := request.(*refreshTokenRequest); ok {
		return refreshReq.ApplicationID, refreshReq.AuthTime, refreshReq.AMR
	}
	return "", time.Time{}, nil
}
