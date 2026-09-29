package oidcprovider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rs/xid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

func testStorage(t *testing.T) (*Storage, *sqlcgen.Queries) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	_, db := dbtest.New(t)
	queries := db.Queries
	storage := newStorage(queries, newSigningKey(key), "https://auth.example.com", 5*time.Minute, time.Hour)
	storage.SetLoginURL(func(id string) string { return "/login?next=" + id })
	return storage, queries
}

func insertTestUser(t *testing.T, queries *sqlcgen.Queries, orgID string) sqlcgen.User {
	t.Helper()
	id := xid.New().String()
	user, err := queries.CreateUser(t.Context(), sqlcgen.CreateUserParams{
		ID:             id,
		Provider:       "oidc",
		ProviderUserID: id,
		Email:          "user@example.com",
		Name:           "Test User",
		AvatarUrl:      "https://example.com/avatar.png",
		EmailVerified:  1,
		OrganizationID: orgID,
		Role:           "owner",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}

func insertTestClient(t *testing.T, queries *sqlcgen.Queries, orgID string) (sqlcgen.OidcClient, string) {
	t.Helper()
	secret := "super-secret"
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	redirectURIs, err := json.Marshal([]string{
		"https://acme.example.com/oidc/callback",
		"https://acme.example.com/admin/oidc/callback",
	})
	if err != nil {
		t.Fatalf("Marshal redirect uris: %v", err)
	}
	client, err := queries.CreateOIDCClient(t.Context(), sqlcgen.CreateOIDCClientParams{
		ID:               xid.New().String(),
		OrganizationID:   orgID,
		ClientID:         "client-id",
		ClientSecretHash: string(hash),
		RedirectUris:     string(redirectURIs),
	})
	if err != nil {
		t.Fatalf("CreateOIDCClient: %v", err)
	}
	return client, secret
}

func TestLoadOrGenerateSigningKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oidc", "signing-key.pem")
	first, err := LoadOrGenerateSigningKey(path)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	second, err := LoadOrGenerateSigningKey(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !reflect.DeepEqual(first.D, second.D) {
		t.Fatal("expected the same private key to be reloaded")
	}
}

type testIDTokenRequest struct {
	subject  string
	clientID string
	scopes   []string
}

func (r testIDTokenRequest) GetAMR() []string       { return []string{"oidc"} }
func (r testIDTokenRequest) GetAudience() []string  { return []string{r.clientID} }
func (r testIDTokenRequest) GetAuthTime() time.Time { return time.Now() }
func (r testIDTokenRequest) GetClientID() string    { return r.clientID }
func (r testIDTokenRequest) GetScopes() []string    { return r.scopes }
func (r testIDTokenRequest) GetSubject() string     { return r.subject }

func TestClientAdapterAndSecretCheck(t *testing.T) {
	storage, queries := testStorage(t)
	org := dbtest.CreateOrg(t, queries, "acme")
	clientRow, secret := insertTestClient(t, queries, org.ID)

	adapter, err := storage.GetClientByClientID(t.Context(), clientRow.ClientID)
	if err != nil {
		t.Fatalf("GetClientByClientID: %v", err)
	}
	if got, want := adapter.RedirectURIs(), []string{"https://acme.example.com/oidc/callback", "https://acme.example.com/admin/oidc/callback"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("redirect uris = %v, want %v", got, want)
	}
	if got, want := adapter.PostLogoutRedirectURIs(), []string{"https://acme.example.com/"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post logout redirect uris = %v, want %v", got, want)
	}
	if err := storage.AuthorizeClientIDSecret(t.Context(), clientRow.ClientID, secret); err != nil {
		t.Fatalf("AuthorizeClientIDSecret(valid): %v", err)
	}
	if err := storage.AuthorizeClientIDSecret(t.Context(), clientRow.ClientID, "wrong"); err == nil {
		t.Fatal("AuthorizeClientIDSecret(wrong): want error")
	}
}

func TestUserClaimsAdapter(t *testing.T) {
	storage, queries := testStorage(t)
	org := dbtest.CreateOrg(t, queries, "acme")
	clientRow, _ := insertTestClient(t, queries, org.ID)
	user := insertTestUser(t, queries, org.ID)

	userInfo := new(oidc.UserInfo)
	if err := storage.SetUserinfoFromRequest(t.Context(), userInfo, testIDTokenRequest{
		subject:  user.ID,
		clientID: clientRow.ClientID,
		scopes:   []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail},
	}, []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail}); err != nil {
		t.Fatalf("SetUserinfoFromRequest: %v", err)
	}
	if userInfo.Subject != user.ID || userInfo.Email != user.Email || userInfo.Name != user.Name || userInfo.Picture != user.AvatarUrl || !bool(userInfo.EmailVerified) {
		t.Fatalf("unexpected userinfo: %+v", userInfo)
	}

	storage.tokens["token-1"] = &token{
		ID:            "token-1",
		ApplicationID: clientRow.ClientID,
		Subject:       user.ID,
		Audience:      []string{clientRow.ClientID},
		Expiration:    time.Now().Add(time.Minute),
		Scopes:        []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail},
	}
	introspection := new(oidc.IntrospectionResponse)
	if err := storage.SetIntrospectionFromToken(t.Context(), introspection, "token-1", user.ID, clientRow.ClientID); err != nil {
		t.Fatalf("SetIntrospectionFromToken: %v", err)
	}
	if introspection.ClientID != clientRow.ClientID || introspection.Subject != user.ID {
		t.Fatalf("unexpected introspection response: %+v", introspection)
	}
}

// TestMembershipGatesAuthRequests checks, a fresh login (AuthorizeAuthRequest)
// and an existing console session (ValidateAuthRequest), only succeed for
// members of the client's organization.
// This one prevents user from logging in to other orgs by running
// tailscale up --login-server=other_org.
func TestMembershipGatesAuthRequests(t *testing.T) {
	storage, queries := testStorage(t)
	acme := dbtest.CreateOrg(t, queries, "acme")
	other := dbtest.CreateOrg(t, queries, "othr")
	clientRow, _ := insertTestClient(t, queries, acme.ID)
	member := insertTestUser(t, queries, acme.ID)
	outsider := insertTestUser(t, queries, other.ID)

	tests := []struct {
		name       string
		userID     string
		wantMember bool
	}{
		{"member", member.ID, true},
		{"user of another organization", outsider.ID, false},
		{"deleted user", "no-such-user", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authReq := &oidc.AuthRequest{
				ClientID:     clientRow.ClientID,
				RedirectURI:  "https://acme.example.com/oidc/callback",
				ResponseType: oidc.ResponseTypeCode,
				Scopes:       []string{oidc.ScopeOpenID},
			}

			req, err := storage.CreateAuthRequest(t.Context(), authReq, "")
			if err != nil {
				t.Fatalf("CreateAuthRequest: %v", err)
			}
			err = storage.AuthorizeAuthRequest(t.Context(), req.GetID(), tt.userID)
			if tt.wantMember && err != nil {
				t.Errorf("AuthorizeAuthRequest: %v", err)
			}
			if !tt.wantMember && !errors.Is(err, ErrNotMember) {
				t.Errorf("AuthorizeAuthRequest error = %v, want ErrNotMember", err)
			}
			if got, _ := storage.AuthRequestByID(t.Context(), req.GetID()); got.Done() != tt.wantMember {
				t.Errorf("Done() = %v, want %v", got.Done(), tt.wantMember)
			}

			provider := &authorizingProvider{storage: storage}
			ctx := context.WithValue(t.Context(), sessionUserKey{}, tt.userID)
			subject, err := provider.ValidateAuthRequest(ctx, authReq, storage, nil)
			if tt.wantMember && (err != nil || subject != tt.userID) {
				t.Errorf("ValidateAuthRequest = %q, %v, want %q", subject, err, tt.userID)
			}
			var oidcErr *oidc.Error
			if !tt.wantMember && (!errors.As(err, &oidcErr) || oidcErr.ErrorType != oidc.AccessDenied) {
				t.Errorf("ValidateAuthRequest error = %v, want access denied", err)
			}
		})
	}
}
