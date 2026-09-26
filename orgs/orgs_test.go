package orgs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/ownscalenet/ownscale/config"
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

	stored, err := ParseHeadscaleFileConfig(cfgRow.Content)
	if err != nil {
		t.Fatalf("ParseHeadscaleFileConfig: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(clientRow.ClientSecretHash), []byte(stored.OIDC.ClientSecret)); err != nil {
		t.Fatalf("stored client secret hash does not match rendered config secret: %v", err)
	}
	if stored.OIDC.Issuer != "https://auth.example.com" || stored.OIDC.ClientID != clientRow.ClientID {
		t.Fatalf("stored OIDC = %+v, want issuer https://auth.example.com and client id %q", stored.OIDC, clientRow.ClientID)
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

func TestStoredConfigRoundTrip(t *testing.T) {
	built, err := BuildHeadscaleConfig("acme", OIDCConfig{Issuer: "https://auth.example.com", ClientID: "os_acme_x", ClientSecret: "secret"})
	if err != nil {
		t.Fatalf("BuildHeadscaleConfig: %v", err)
	}

	cfg, err := ParseHeadscaleFileConfig(built)
	if err != nil {
		t.Fatalf("ParseHeadscaleFileConfig: %v", err)
	}
	if cfg.DNS.BaseDomain != "acme.ownscale" || !cfg.DNS.MagicDNS || !slices.Equal(cfg.DNS.Nameservers.Global, []string{"1.1.1.1"}) {
		t.Errorf("DNS = %+v", cfg.DNS)
	}
	if cfg.OIDC.Issuer != "https://auth.example.com" || cfg.OIDC.ClientID != "os_acme_x" || cfg.OIDC.ClientSecret != "secret" {
		t.Errorf("OIDC = %+v", cfg.OIDC)
	}
	if !slices.Contains(cfg.OIDC.Scope, "openid") {
		t.Errorf("OIDC scope = %v, want openid", cfg.OIDC.Scope)
	}
}

func TestCreateHeadscaleDERP(t *testing.T) {
	custom := config.DerpConfig{
		Hostname: "derp.example.com", IPv4: "203.0.113.5", STUNPort: 3478,
		RegionID: 900, RegionCode: "custom", RegionName: "Custom",
	}
	tests := []struct {
		name       string
		derp       config.DerpConfig
		wantRegion bool
	}{
		{"no custom relay", config.DerpConfig{}, false},
		{"custom relay", custom, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fileCfg := newFileConfig("acme", OIDCConfig{Issuer: "https://auth.example.com"})
			app, err := createHeadscale(t.Context(), t.TempDir(), "acme", "example.com", 0, fileCfg, tt.derp)
			if err != nil {
				t.Fatalf("createHeadscale: %v", err)
			}
			t.Cleanup(func() { _ = app.GetState().Close() })

			dm := app.GetState().DebugConfig().DERP.DERPMap
			if !tt.wantRegion {
				if dm != nil {
					t.Fatalf("DERPMap = %+v, want nil", dm)
				}
				return
			}
			region := dm.Regions[900]
			if region == nil || region.RegionCode != "custom" || len(region.Nodes) != 1 {
				t.Fatalf("region 900 = %+v", region)
			}
			if n := region.Nodes[0]; n.Name != "customa" || n.HostName != "derp.example.com" || n.IPv4 != "203.0.113.5" || n.STUNPort != 3478 || n.DERPPort != 0 {
				t.Errorf("node = %+v", n)
			}
		})
	}
}
