package orgs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// newAuthRestrictionsTestService builds a Service for testing auth
// restrictions and DNS settings, backed by a real temp sqlite DB.
func newAuthRestrictionsTestService(t *testing.T) *Service {
	t.Helper()
	return newTestService(t)
}

// TestGetAuthRestrictionsDefaultsToEmptyWhenMissing verifies
// GetAuthRestrictions doesn't error for an organization with no
// orgconfigs row (e.g. a brand new one, or one provisioned before this
// feature existed), returning defaultAuthRestrictions instead.
func TestGetAuthRestrictionsDefaultsToEmptyWhenMissing(t *testing.T) {
	svc := newAuthRestrictionsTestService(t)

	content, err := svc.ForOrg(sqlcgen.Organization{ID: "nonexistent-org-id"}).GetAuthRestrictions(t.Context())
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != defaultAuthRestrictions {
		t.Fatalf("content = %q, want default", content)
	}
}

// TestSetAuthRestrictionsRejectsInvalidHuJSON verifies SetAuthRestrictions
// validates content before persisting.
func TestSetAuthRestrictionsRejectsInvalidHuJSON(t *testing.T) {
	svc := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), "not valid hujson {{{"); err == nil {
		t.Fatal("SetAuthRestrictions: want error for invalid HuJSON, got nil")
	}
}

func TestSetAuthRestrictionsPersistsAndMarksActive(t *testing.T) {
	svc := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newContent := `{"domains":["example.com"],"groups":["group:eng"],"users":["alice@example.com"]}`
	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), newContent); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}

	content, err := svc.ForOrg(org).GetAuthRestrictions(t.Context())
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != newContent {
		t.Fatalf("GetAuthRestrictions = %q, want %q", content, newContent)
	}

	// org status should be active after a successful config change.
	got, err := svc.ForOrg(org).queries.GetOrganization(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if got.Status != StatusActive {
		t.Errorf("org.Status = %q, want %q", got.Status, StatusActive)
	}
}

// TestSetAuthRestrictionsCanClearAllowlist verifies saving an empty
// allowlist after a previous non-empty save clears the stored one.
func TestSetAuthRestrictionsCanClearAllowlist(t *testing.T) {
	svc := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), `{"domains":["example.com"]}`); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}
	emptyContent := `{"domains":[],"groups":[],"users":[]}`
	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), emptyContent); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}

	content, err := svc.ForOrg(org).GetAuthRestrictions(t.Context())
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != emptyContent {
		t.Fatalf("GetAuthRestrictions = %q, want %q", content, emptyContent)
	}
}

func TestGetDNSSettingsReflectsProvisionedDefaults(t *testing.T) {
	svc := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	dns, err := svc.ForOrg(org).GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}
	if !dns.MagicDNS {
		t.Errorf("MagicDNS = false, want true")
	}
	if dns.BaseDomain != "acme.ownscale" {
		t.Errorf("BaseDomain = %q, want %q", dns.BaseDomain, "acme.ownscale")
	}
}

func TestSetDNSSettingsPersistsAndMarksActive(t *testing.T) {
	svc := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := DNSConfig{
		BaseDomain:    "acme.headscale.internal",
		MagicDNS:      true,
		Nameservers:   Nameservers{Global: []string{"1.1.1.1", "8.8.8.8"}},
		SearchDomains: []string{"internal.acme.example.com"},
		ExtraRecords:  []DNSRecord{{Name: "server.acme.headscale.internal", Type: "A", Value: "10.0.0.1"}},
	}
	if err := svc.ForOrg(org).SetDNSSettings(t.Context(), want); err != nil {
		t.Fatalf("SetDNSSettings: %v", err)
	}

	got, err := svc.ForOrg(org).GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}
	if len(got.Nameservers.Global) != 2 || got.Nameservers.Global[1] != "8.8.8.8" {
		t.Errorf("Nameservers = %v, want %v", got.Nameservers.Global, want.Nameservers.Global)
	}
	if len(got.ExtraRecords) != 1 || got.ExtraRecords[0] != want.ExtraRecords[0] {
		t.Errorf("ExtraRecords = %v, want %v", got.ExtraRecords, want.ExtraRecords)
	}

	orgAfter, err := svc.ForOrg(org).queries.GetOrganization(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if orgAfter.Status != StatusActive {
		t.Errorf("org.Status = %q, want %q", orgAfter.Status, StatusActive)
	}
}

func TestSetDNSSettingsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		dns  DNSConfig
	}{
		{"empty base domain", DNSConfig{BaseDomain: ""}},
		{"empty record name", DNSConfig{BaseDomain: "acme.headscale.internal", ExtraRecords: []DNSRecord{{Name: "", Type: "A", Value: "1.2.3.4"}}}},
		{"empty record value", DNSConfig{BaseDomain: "acme.headscale.internal", ExtraRecords: []DNSRecord{{Name: "a.example.com", Type: "A", Value: ""}}}},
		{"duplicate record", DNSConfig{BaseDomain: "acme.headscale.internal", ExtraRecords: []DNSRecord{
			{Name: "a.example.com", Type: "A", Value: "1.2.3.4"},
			{Name: "a.example.com", Type: "A", Value: "5.6.7.8"},
		}}},
	}

	svc := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.ForOrg(org).SetDNSSettings(t.Context(), tt.dns)
			if err == nil {
				t.Fatal("SetDNSSettings: want error, got nil")
			}
			if _, ok := errors.AsType[*InvalidConfigError](err); !ok {
				t.Fatalf("SetDNSSettings error = %v, want *InvalidConfigError", err)
			}
		})
	}
}

// TestSetDNSSettingsRollsBackOnInvalidConfig verifies that setting DNS
// settings with an invalid config (caught by in-process validation) does
// not mutate the stored config.
func TestSetDNSSettingsRollsBackOnInvalidConfig(t *testing.T) {
	svc := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	original, err := svc.ForOrg(org).GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}

	// empty BaseDomain is rejected by validateDNSSettings.
	bad := DNSConfig{BaseDomain: "", MagicDNS: true}
	err = svc.ForOrg(org).SetDNSSettings(t.Context(), bad)
	if err == nil {
		t.Fatal("SetDNSSettings: want error for empty BaseDomain, got nil")
	}
	if _, ok := errors.AsType[*InvalidConfigError](err); !ok {
		t.Fatalf("SetDNSSettings error = %v, want *InvalidConfigError", err)
	}

	got, err := svc.ForOrg(org).GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("GetDNSSettings after rejected update = %+v, want unchanged %+v", got, original)
	}
}

// TestSetDNSSettingsUpdatesRunningHeadscaleConfig verifies that after
// SetDNSSettings the running headscale instance's TailcfgDNSConfig is
// updated to reflect the new nameserver without a process restart.
func TestSetDNSSettingsUpdatesRunningHeadscaleConfig(t *testing.T) {
	// Use the real createFn (createHeadscale) -- not the noop -- so a real
	// headscale instance is started and its runtime state can be inspected.
	queries := newTestQueries(t)
	svc := NewService(queries, "example.com", "https://auth.example.com", config.DerpConfig{}, discardLogger(), t.TempDir())

	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	port, ok := svc.LookupPort("acme")
	if !ok {
		t.Fatal("LookupPort: no port registered for acme")
	}
	app, ok := svc.instances.Load("acme")
	if !ok {
		t.Fatal("instances.Load: headscale instance not found after Create")
	}

	waitCtx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := waitHeadscaleReady(waitCtx, port); err != nil {
		t.Fatalf("headscale did not become ready on port %d: %v", port, err)
	}

	if err := svc.ForOrg(org).SetDNSSettings(t.Context(), DNSConfig{
		BaseDomain:  "acme.ownscale",
		MagicDNS:    true,
		Nameservers: Nameservers{Global: []string{"1.2.3.4"}},
	}); err != nil {
		t.Fatalf("SetDNSSettings: %v", err)
	}

	// Assert: the running headscale instance's DNS config was updated.
	tailcfgDNS := app.GetState().DebugConfig().TailcfgDNSConfig
	if tailcfgDNS == nil {
		t.Fatal("TailcfgDNSConfig is nil")
	}
	// Nameservers map to FallbackResolvers when OverrideLocalDNS is false.
	var found bool
	for _, r := range tailcfgDNS.FallbackResolvers {
		if r != nil && r.Addr == "1.2.3.4" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("resolver 1.2.3.4 not found in running headscale TailcfgDNSConfig.FallbackResolvers after SetDNSSettings; got %v", tailcfgDNS.FallbackResolvers)
	}
}

// waitHeadscaleReady polls /health until it returns 200 or ctx expires.
func waitHeadscaleReady(ctx context.Context, port int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
