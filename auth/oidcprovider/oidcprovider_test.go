package oidcprovider

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
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

// newTestQueries returns queries backed by a copy of the shared, already
// migrated dbtest template (see db/dbtest), avoiding a full goose run per
// test.
func newTestQueries(t *testing.T) *sqlcgen.Queries {
	t.Helper()
	_, queries := dbtest.New(t)
	return queries.Queries
}

func testStorage(t *testing.T) (*Storage, *sqlcgen.Queries) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	queries := newTestQueries(t)
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

func insertTestOrg(t *testing.T, queries *sqlcgen.Queries) sqlcgen.Organization {
	t.Helper()
	org, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: "acme",
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	return org
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
	org := insertTestOrg(t, queries)
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
	org := insertTestOrg(t, queries)
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
