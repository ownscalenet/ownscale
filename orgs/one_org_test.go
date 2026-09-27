package orgs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ownscalenet/headscale/hscontrol"

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

// TestSettingsRestartHeadscale verifies that saving settings recreates the
// org's headscale from the stored config and leaves the org active.
func TestSettingsRestartHeadscale(t *testing.T) {
	tests := []struct {
		name  string
		apply func(t *testing.T, o *OrgService)
		check func(t *testing.T, cfg *HeadscaleConfig)
	}{
		{
			name: "dns",
			apply: func(t *testing.T, o *OrgService) {
				err := o.SetDNSSettings(t.Context(), DNSConfig{
					BaseDomain:  "acme.ownscale",
					MagicDNS:    true,
					Nameservers: Nameservers{Global: []string{"1.2.3.4"}},
				})
				if err != nil {
					t.Fatalf("SetDNSSettings: %v", err)
				}
			},
			check: func(t *testing.T, cfg *HeadscaleConfig) {
				if got := cfg.DNS.Nameservers.Global; !slices.Equal(got, []string{"1.2.3.4"}) {
					t.Errorf("nameservers = %v, want [1.2.3.4]", got)
				}
			},
		},
		{
			name: "auth restrictions",
			apply: func(t *testing.T, o *OrgService) {
				if err := o.SetAuthRestrictions(t.Context(), `{"domains":["example.com"]}`); err != nil {
					t.Fatalf("SetAuthRestrictions: %v", err)
				}
			},
			check: func(t *testing.T, cfg *HeadscaleConfig) {
				if got := cfg.OIDC.AllowedDomains; !slices.Equal(got, []string{"example.com"}) {
					t.Errorf("allowed domains = %v, want [example.com]", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			org, err := svc.Create(t.Context(), "acme")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			var got []*HeadscaleConfig
			svc.createFn = func(_, _, _ string, _ int, cfg *HeadscaleConfig, _ config.DerpConfig) (*hscontrol.Headscale, error) {
				got = append(got, cfg)
				return nil, nil
			}

			tt.apply(t, svc.ForOrg(org))

			if len(got) != 1 {
				t.Fatalf("createFn called %d times, want 1", len(got))
			}
			tt.check(t, got[0])
			updated, err := svc.queries.GetOrganization(t.Context(), org.ID)
			if err != nil {
				t.Fatalf("GetOrganization: %v", err)
			}
			if updated.Status != StatusActive {
				t.Errorf("status = %q, want %q", updated.Status, StatusActive)
			}
		})
	}
}

// TestSettingsRestartFailureLeavesUpdatePending verifies a failed restart is
// reported and the org stays update_pending, so startup recovery retries it.
func TestSettingsRestartFailureLeavesUpdatePending(t *testing.T) {
	svc := newTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	svc.createFn = func(string, string, string, int, *HeadscaleConfig, config.DerpConfig) (*hscontrol.Headscale, error) {
		return nil, errors.New("headscale failed (injected)")
	}

	if err := svc.ForOrg(org).SetAuthRestrictions(t.Context(), `{"domains":["example.com"]}`); err == nil {
		t.Fatal("SetAuthRestrictions: want error when restart fails, got nil")
	}
	updated, err := svc.queries.GetOrganization(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if updated.Status != StatusUpdatePending {
		t.Errorf("status = %q, want %q", updated.Status, StatusUpdatePending)
	}
}

// TestSettingsApplyToRunningHeadscale uses real headscale instances to
// verify saved settings reach the restarted server, which keeps its port.
func TestSettingsApplyToRunningHeadscale(t *testing.T) {
	svc := newRealService(t)

	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		if inst, ok := svc.instances.Load("acme"); ok {
			inst.cancel()
			<-inst.exited
		}
	})
	port, _ := svc.LookupPort("acme")
	first, _ := svc.instances.Load("acme")

	waitCtx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := waitHeadscaleReady(waitCtx, port); err != nil {
		t.Fatalf("headscale did not become ready on port %d: %v", port, err)
	}

	o := svc.ForOrg(org)
	if err := o.SetDNSSettings(t.Context(), DNSConfig{
		BaseDomain:  "acme.ownscale",
		MagicDNS:    true,
		Nameservers: Nameservers{Global: []string{"1.2.3.4"}},
	}); err != nil {
		t.Fatalf("SetDNSSettings: %v", err)
	}
	if err := o.SetAuthRestrictions(t.Context(), `{"domains":["example.com"]}`); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}

	second, _ := svc.instances.Load("acme")
	if second == first {
		t.Fatal("headscale instance was not replaced")
	}
	<-first.exited
	if p, _ := svc.LookupPort("acme"); p != port {
		t.Errorf("port after restart = %d, want %d", p, port)
	}
	if err := waitHeadscaleReady(waitCtx, port); err != nil {
		t.Fatalf("restarted headscale not ready on port %d: %v", port, err)
	}
	client, err := svc.Client("acme")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if _, err := client.ListUsers(t.Context()); err != nil {
		t.Errorf("ListUsers over the restarted instance's socket: %v", err)
	}

	hs := second.app.GetState().DebugConfig()
	// Nameservers map to FallbackResolvers when OverrideLocalDNS is false.
	if dns := hs.TailcfgDNSConfig; dns == nil || len(dns.FallbackResolvers) != 1 || dns.FallbackResolvers[0].Addr != "1.2.3.4" {
		t.Errorf("TailcfgDNSConfig = %+v, want resolver 1.2.3.4", dns)
	}
	if !slices.Equal(hs.OIDC.AllowedDomains, []string{"example.com"}) {
		t.Errorf("OIDC allowed domains = %v, want [example.com]", hs.OIDC.AllowedDomains)
	}
	// Only ownscale's own proxy needs to reach an org's headscale.
	if !strings.HasPrefix(hs.Addr, "127.0.0.1:") {
		t.Errorf("headscale listen address = %q, want loopback only", hs.Addr)
	}
	// Without scopes the OIDC provider rejects headscale's login requests.
	if !slices.Contains(hs.OIDC.Scope, "openid") {
		t.Errorf("OIDC scope = %v, want it to include openid", hs.OIDC.Scope)
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
