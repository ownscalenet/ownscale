package orgs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// newTestQueries returns queries backed by a copy of the shared, already
// migrated dbtest template (see db/dbtest), avoiding a full goose run per
// test.
func newTestQueries(t *testing.T) *dbpkg.TxQueries {
	t.Helper()
	_, queries := dbtest.New(t)
	return queries
}

// TestServiceCreateCreatesOIDCClient verifies that Create inserts an OIDC
// client row and writes the headscale config YAML (with the correct issuer
// and a bcrypt-hashed client secret) into the orgconfigs table.
func TestServiceCreateCreatesOIDCClient(t *testing.T) {
	svc := newTestService(t)
	queries := svc.queries

	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// OIDC client row must exist.
	clientRow, err := queries.GetOIDCClientByOrganizationID(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetOIDCClientByOrganizationID: %v", err)
	}
	if !strings.HasPrefix(clientRow.ClientID, "os_acme_") {
		t.Fatalf("client id = %q, want os_acme_*", clientRow.ClientID)
	}
	var redirectURIs []string
	if err := json.Unmarshal([]byte(clientRow.RedirectUris), &redirectURIs); err != nil {
		t.Fatalf("Unmarshal redirect uris: %v", err)
	}
	wantRedirects := []string{
		"https://acme.example.com/oidc/callback",
		"https://acme.example.com/admin/oidc/callback",
	}
	if strings.Join(redirectURIs, ",") != strings.Join(wantRedirects, ",") {
		t.Fatalf("redirect uris = %v, want %v", redirectURIs, wantRedirects)
	}

	// orgconfigs must have a config.yaml row with the correct issuer and a
	// client_secret whose plaintext matches the stored bcrypt hash.
	cfgRow, err := queries.GetOrgConfig(t.Context(), sqlcgen.GetOrgConfigParams{
		OrganizationID: org.ID,
		Path:           ConfigYAMLPath,
	})
	if err != nil {
		t.Fatalf("GetOrgConfig: %v", err)
	}

	secretLine := findLine(cfgRow.Content, `  client_secret: "`)
	secret := strings.TrimSuffix(strings.TrimPrefix(secretLine, `  client_secret: "`), `"`)
	if err := bcrypt.CompareHashAndPassword([]byte(clientRow.ClientSecretHash), []byte(secret)); err != nil {
		t.Fatalf("stored client secret hash does not match rendered config secret: %v", err)
	}
	if !strings.Contains(cfgRow.Content, `issuer: "https://auth.example.com"`) {
		t.Fatalf("headscale config missing issuer:\n%s", cfgRow.Content)
	}
}

// TestServiceCreateDoesNotLeavePartialStateOnSubdomainConflict verifies
// that Create returns an error and leaves exactly one org row when a
// second Create is attempted with the same subdomain.
func TestServiceCreateDoesNotLeavePartialStateOnSubdomainConflict(t *testing.T) {
	svc := newTestService(t)
	queries := svc.queries

	if _, err := svc.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Second Create with same subdomain must fail.
	if _, err := svc.Create(t.Context(), "acme"); err == nil {
		t.Fatal("second Create with duplicate subdomain: want error, got nil")
	}
	// Exactly one org row should exist.
	org, err := queries.GetOrganizationBySubdomain(t.Context(), "acme")
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatal("GetOrganizationBySubdomain: want exactly one row, got none")
	}
	if err != nil {
		t.Fatalf("GetOrganizationBySubdomain: %v", err)
	}
	if org.Subdomain != "acme" {
		t.Errorf("org.Subdomain = %q, want %q", org.Subdomain, "acme")
	}
}

func findLine(s, prefix string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}
