package orgs

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/ownscalenet/headscale/hscontrol"
	"github.com/ownscalenet/headscale/hscontrol/types"
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
// client row and stores the organization's HeadscaleConfig (with the correct issuer
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

	// orgconfigs must have a HeadscaleConfig row with the correct issuer and a
	// client_secret whose plaintext matches the stored bcrypt hash.
	cfgRow, err := queries.GetOrgConfig(t.Context(), sqlcgen.GetOrgConfigParams{
		OrganizationID: org.ID,
		Path:           HeadscaleConfigKey,
	})
	if err != nil {
		t.Fatalf("GetOrgConfig: %v", err)
	}

	stored, err := parseHeadscaleConfig(cfgRow.Content)
	if err != nil {
		t.Fatalf("parseHeadscaleConfig: %v", err)
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

	first, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// A duplicate must be rejected before a second headscale for "acme"
	// replaces the running one.
	svc.createFn = func(string, string, string, int, *HeadscaleConfig, config.DerpConfig) (*hscontrol.Headscale, error) {
		t.Error("headscale started for a duplicate organization")
		return nil, nil
	}
	if _, err := svc.Create(t.Context(), "acme"); err == nil {
		t.Fatal("second Create with duplicate subdomain: want error, got nil")
	}
	org, err := queries.GetOrganizationBySubdomain(t.Context(), "acme")
	if err != nil {
		t.Fatalf("GetOrganizationBySubdomain: %v", err)
	}
	if org.ID != first.ID {
		t.Errorf("organization ID = %q, want the first one %q", org.ID, first.ID)
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
			hsCfg := newHeadscaleConfig("acme", OIDCConfig{Issuer: "https://auth.example.com"})
			app, err := createHeadscale(t.TempDir(), "acme", "example.com", 0, hsCfg, tt.derp)
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

// newRealService builds a Service that starts real headscale instances.
func newRealService(t *testing.T) *Service {
	t.Helper()
	return NewService(newTestQueries(t), "example.com", "https://auth.example.com", config.DerpConfig{}, discardLogger(), t.TempDir())
}

func TestSetOIDCIdentity(t *testing.T) {
	svc := newRealService(t)
	if _, err := svc.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	inst, _ := svc.instances.Load("acme")
	t.Cleanup(func() { inst.cancel(); <-inst.exited })
	state := inst.app.GetState()

	newUser := func(name string) string {
		u, _, err := state.CreateUser(types.User{Name: name, Email: name + "@example.com", DisplayName: name})
		if err != nil {
			t.Fatalf("CreateUser(%q): %v", name, err)
		}
		return strconv.FormatUint(uint64(u.ID), 10)
	}
	alice, bob := newUser("alice"), newUser("bob")
	identifier := (&types.OIDCClaims{Iss: "https://auth.example.com", Sub: "sub-1"}).Identifier()

	if err := svc.SetOIDCIdentity(t.Context(), "acme", alice, "sub-1"); err != nil {
		t.Fatalf("SetOIDCIdentity: %v", err)
	}
	got, err := state.GetUserByOIDCIdentifier(identifier)
	if err != nil {
		t.Fatalf("GetUserByOIDCIdentifier: %v", err)
	}
	if strconv.FormatUint(uint64(got.ID), 10) != alice || got.Name != "alice" || got.Email != "alice@example.com" || got.Provider != "oidc" {
		t.Errorf("user = %+v, want alice unchanged apart from the OIDC identity", got)
	}

	// A user that already has an identity, and an identity owned by
	// another user, are both left alone.
	if err := svc.SetOIDCIdentity(t.Context(), "acme", alice, "sub-2"); err != nil {
		t.Fatalf("SetOIDCIdentity again: %v", err)
	}
	if err := svc.SetOIDCIdentity(t.Context(), "acme", bob, "sub-1"); err != nil {
		t.Fatalf("SetOIDCIdentity taken identity: %v", err)
	}
	bobUser, err := state.GetUserByID(types.UserID(mustParseUint(t, bob)))
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if bobUser.ProviderIdentifier.Valid {
		t.Errorf("bob got identity %q, want none", bobUser.ProviderIdentifier.String)
	}

	if err := svc.SetOIDCIdentity(t.Context(), "missing", alice, "sub-1"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("unknown org error = %v, want ErrNotRunning", err)
	}
}

func mustParseUint(t *testing.T, s string) uint {
	t.Helper()
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return uint(n)
}
