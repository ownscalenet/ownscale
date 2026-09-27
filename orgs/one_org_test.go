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

// newTestOrgService creates organization "acme" on a newTestService and
// returns both.
func newTestOrgService(t *testing.T) (*Service, *OrgService) {
	t.Helper()
	svc := newTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return svc, svc.ForOrg(org)
}

// assertStatus fails t unless o's organization has status want.
func assertStatus(t *testing.T, o *OrgService, want string) {
	t.Helper()
	org, err := o.queries.GetOrganization(t.Context(), o.orgID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if org.Status != want {
		t.Errorf("status = %q, want %q", org.Status, want)
	}
}

// TestGetAuthRestrictionsDefaultsToEmptyWhenMissing verifies
// GetAuthRestrictions returns defaultAuthRestrictions for an organization
// that never saved any.
func TestGetAuthRestrictionsDefaultsToEmptyWhenMissing(t *testing.T) {
	svc := newTestService(t)

	content, err := svc.ForOrg(sqlcgen.Organization{ID: "nonexistent-org-id"}).GetAuthRestrictions(t.Context())
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != defaultAuthRestrictions {
		t.Fatalf("content = %q, want default", content)
	}
}

func TestSetAuthRestrictionsRejectsInvalidHuJSON(t *testing.T) {
	_, o := newTestOrgService(t)

	if err := o.SetAuthRestrictions(t.Context(), "not valid hujson {{{"); err == nil {
		t.Fatal("SetAuthRestrictions: want error for invalid HuJSON, got nil")
	}
}

// TestSetAuthRestrictions verifies the last saved allowlist is returned
// as written and the org ends up active.
func TestSetAuthRestrictions(t *testing.T) {
	tests := []struct {
		name  string
		saves []string
	}{
		{"persists", []string{`{"domains":["example.com"],"groups":["group:eng"],"users":["alice@example.com"]}`}},
		{"clears allowlist", []string{`{"domains":["example.com"]}`, `{"domains":[],"groups":[],"users":[]}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, o := newTestOrgService(t)
			for _, content := range tt.saves {
				if err := o.SetAuthRestrictions(t.Context(), content); err != nil {
					t.Fatalf("SetAuthRestrictions(%q): %v", content, err)
				}
			}

			got, err := o.GetAuthRestrictions(t.Context())
			if err != nil {
				t.Fatalf("GetAuthRestrictions: %v", err)
			}
			if want := tt.saves[len(tt.saves)-1]; got != want {
				t.Errorf("GetAuthRestrictions = %q, want %q", got, want)
			}
			assertStatus(t, o, StatusActive)
		})
	}
}

func TestGetDNSSettingsReflectsProvisionedDefaults(t *testing.T) {
	_, o := newTestOrgService(t)

	dns, err := o.GetDNSSettings(t.Context())
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
	_, o := newTestOrgService(t)

	want := DNSConfig{
		BaseDomain:    "acme.headscale.internal",
		MagicDNS:      true,
		Nameservers:   Nameservers{Global: []string{"1.1.1.1", "8.8.8.8"}},
		SearchDomains: []string{"internal.acme.example.com"},
		ExtraRecords:  []DNSRecord{{Name: "server.acme.headscale.internal", Type: "A", Value: "10.0.0.1"}},
	}
	if err := o.SetDNSSettings(t.Context(), want); err != nil {
		t.Fatalf("SetDNSSettings: %v", err)
	}

	got, err := o.GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetDNSSettings = %+v, want %+v", got, want)
	}
	assertStatus(t, o, StatusActive)
}

// TestSetDNSSettingsRejectsInvalidInput verifies invalid settings are
// rejected with an InvalidConfigError and leave the stored ones unchanged.
func TestSetDNSSettingsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		dns  DNSConfig
	}{
		{"empty base domain", DNSConfig{BaseDomain: "", MagicDNS: true}},
		{"empty record name", DNSConfig{BaseDomain: "acme.headscale.internal", ExtraRecords: []DNSRecord{{Name: "", Type: "A", Value: "1.2.3.4"}}}},
		{"empty record value", DNSConfig{BaseDomain: "acme.headscale.internal", ExtraRecords: []DNSRecord{{Name: "a.example.com", Type: "A", Value: ""}}}},
		{"duplicate record", DNSConfig{BaseDomain: "acme.headscale.internal", ExtraRecords: []DNSRecord{
			{Name: "a.example.com", Type: "A", Value: "1.2.3.4"},
			{Name: "a.example.com", Type: "A", Value: "5.6.7.8"},
		}}},
	}

	_, o := newTestOrgService(t)
	original, err := o.GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := o.SetDNSSettings(t.Context(), tt.dns)
			if _, ok := errors.AsType[*InvalidConfigError](err); !ok {
				t.Fatalf("SetDNSSettings error = %v, want *InvalidConfigError", err)
			}
			got, err := o.GetDNSSettings(t.Context())
			if err != nil {
				t.Fatalf("GetDNSSettings: %v", err)
			}
			if !reflect.DeepEqual(got, original) {
				t.Errorf("GetDNSSettings after rejected update = %+v, want unchanged %+v", got, original)
			}
		})
	}
}

// TestSettingsRestartFailureLeavesUpdatePending verifies a failed restart is
// reported and the org stays update_pending, so startup recovery retries it.
func TestSettingsRestartFailureLeavesUpdatePending(t *testing.T) {
	svc, o := newTestOrgService(t)
	svc.createFn = func(string, string, string, int, *HeadscaleConfig, config.DerpConfig) (*hscontrol.Headscale, error) {
		return nil, errors.New("headscale failed (injected)")
	}

	if err := o.SetAuthRestrictions(t.Context(), `{"domains":["example.com"]}`); err == nil {
		t.Fatal("SetAuthRestrictions: want error when restart fails, got nil")
	}
	assertStatus(t, o, StatusUpdatePending)
}

// TestSettingsApplyToRunningHeadscale uses real headscale instances to
// verify saved settings reach the restarted server, which keeps its port,
// and leave the org active.
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
	if err := waitHeadscaleReady(waitCtx, port, first.exited); err != nil {
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
	assertStatus(t, o, StatusActive)

	second, _ := svc.instances.Load("acme")
	if second == first {
		t.Fatal("headscale instance was not replaced")
	}
	<-first.exited
	if p, _ := svc.LookupPort("acme"); p != port {
		t.Errorf("port after restart = %d, want %d", p, port)
	}
	if err := waitHeadscaleReady(waitCtx, port, second.exited); err != nil {
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

// waitHeadscaleReady polls /health until it returns 200, the instance
// exits, or ctx expires.
func waitHeadscaleReady(ctx context.Context, port int, exited <-chan struct{}) error {
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
		case <-exited:
			return errors.New("headscale exited")
		case <-time.After(200 * time.Millisecond):
		}
	}
}
