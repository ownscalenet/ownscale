package orgs

import (
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/juanfont/headscale/hscontrol"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// newTestOrgService creates and starts organization "acme" on a
// newTestService and returns both.
func newTestOrgService(t *testing.T) (*Service, *OrgService) {
	t.Helper()
	svc := newTestService(t)
	return svc, svc.ForOrg(createStarted(t, svc, "acme"))
}

// assertStatus fails t unless svc reports status want for "acme".
func assertStatus(t *testing.T, svc *Service, want string) {
	t.Helper()
	if got := svc.Status("acme"); got != want {
		t.Errorf("status = %q, want %q", got, want)
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
// as written.
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
	if !regexp.MustCompile(`^net[a-z0-9]{6}\.os\.example\.com$`).MatchString(dns.BaseDomain) {
		t.Errorf("BaseDomain = %q, want net<6 chars>.os.example.com", dns.BaseDomain)
	}
}

func TestSetDNSSettingsPersistsAndMarksActive(t *testing.T) {
	_, o := newTestOrgService(t)

	want := DNSConfig{
		BaseDomain: o.domain,
		MagicDNS:   true,
		Nameservers: Nameservers{
			Global: []string{"1.1.1.1", "8.8.8.8"},
			Split: map[string][]string{
				"corp.example.com": {"10.0.0.53", "https://dns.example.com/dns-query"},
			},
		},
		SearchDomains: []string{"internal.acme.example.com"},
		ExtraRecords:  []DNSRecord{{Name: "server.acme.headscale.internal", Type: "A", Value: "10.0.0.1"}},
	}
	// Split domains are stored in canonical form; the base domain is fixed.
	in := want
	in.BaseDomain = "ignored.example.com"
	in.Nameservers.Split = map[string][]string{
		" Corp.Example.COM. ": {"10.0.0.53", "https://dns.example.com/dns-query"},
	}
	if err := o.SetDNSSettings(t.Context(), in); err != nil {
		t.Fatalf("SetDNSSettings: %v", err)
	}

	got, err := o.GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetDNSSettings = %+v, want %+v", got, want)
	}
}

// TestSetDNSSettingsRejectsInvalidInput verifies invalid settings are
// rejected with an InvalidConfigError and leave the stored ones unchanged.
func TestSetDNSSettingsRejectsInvalidInput(t *testing.T) {
	_, o := newTestOrgService(t)
	original, err := o.GetDNSSettings(t.Context())
	if err != nil {
		t.Fatalf("GetDNSSettings: %v", err)
	}

	tests := []struct {
		name string
		dns  DNSConfig
	}{
		{"empty record name", DNSConfig{ExtraRecords: []DNSRecord{{Name: "", Type: "A", Value: "1.2.3.4"}}}},
		{"empty record value", DNSConfig{ExtraRecords: []DNSRecord{{Name: "a.example.com", Type: "A", Value: ""}}}},
		{"duplicate record", DNSConfig{ExtraRecords: []DNSRecord{
			{Name: "a.example.com", Type: "A", Value: "1.2.3.4"},
			{Name: "a.example.com", Type: "A", Value: "5.6.7.8"},
		}}},
		{"invalid global nameserver", withNameservers(Nameservers{Global: []string{"not-an-ip"}})},
		{"global nameserver with port", withNameservers(Nameservers{Global: []string{"1.1.1.1:5353"}})},
		{"plain http nameserver", withNameservers(Nameservers{Global: []string{"http://dns.example.com/dns-query"}})},
		{"empty split domain", withSplit(map[string][]string{"": {"10.0.0.53"}})},
		{"root split domain", withSplit(map[string][]string{".": {"10.0.0.53"}})},
		{"invalid split domain", withSplit(map[string][]string{"corp..example.com": {"10.0.0.53"}})},
		{"split domain with bad label", withSplit(map[string][]string{"-corp.example.com": {"10.0.0.53"}})},
		{"split domain is the tailnet name", withSplit(map[string][]string{o.domain: {"10.0.0.53"}})},
		{"split domain inside the tailnet name", withSplit(map[string][]string{"db." + o.domain: {"10.0.0.53"}})},
		{"split domain without nameservers", withSplit(map[string][]string{"corp.example.com": {}})},
		{"invalid split nameserver", withSplit(map[string][]string{"corp.example.com": {"corp-dns"}})},
		{"duplicate split domain", withSplit(map[string][]string{
			"corp.example.com":  {"10.0.0.53"},
			"CORP.example.com.": {"10.0.0.54"},
		})},
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

func withNameservers(ns Nameservers) DNSConfig {
	return DNSConfig{Nameservers: ns}
}

func withSplit(split map[string][]string) DNSConfig {
	return withNameservers(Nameservers{Global: []string{"1.1.1.1"}, Split: split})
}

// TestSettingsRestartFailureLeavesUnavailable verifies a failed restart is
// reported and leaves the org unavailable.
func TestSettingsRestartFailureLeavesUnavailable(t *testing.T) {
	svc, o := newTestOrgService(t)
	svc.createFn = func(headscaleParams) (*hscontrol.Headscale, error) {
		return nil, errors.New("headscale failed (injected)")
	}

	if err := o.SetAuthRestrictions(t.Context(), `{"domains":["example.com"]}`); err == nil {
		t.Fatal("SetAuthRestrictions: want error when restart fails, got nil")
	}
	assertStatus(t, svc, StatusUnavailable)
}

// TestSettingsApplyToRunningHeadscale uses real headscale instances to
// verify saved settings reach the restarted server, which keeps its port,
// and leave the org active.
func TestSettingsApplyToRunningHeadscale(t *testing.T) {
	svc := newRealService(t)
	org := createStarted(t, svc, "acme")
	port, _ := svc.LookupPort("acme")
	first, _ := svc.running("acme")

	o := svc.ForOrg(org)
	if err := o.SetDNSSettings(t.Context(), DNSConfig{
		MagicDNS:    true,
		Nameservers: Nameservers{Global: []string{"1.2.3.4"}},
	}); err != nil {
		t.Fatalf("SetDNSSettings: %v", err)
	}
	if err := o.SetAuthRestrictions(t.Context(), `{"domains":["example.com"]}`); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}
	assertStatus(t, svc, StatusActive)

	second, _ := svc.running("acme")
	if second == first {
		t.Fatal("headscale instance was not replaced")
	}
	<-first.exited
	if p, _ := svc.LookupPort("acme"); p != port {
		t.Errorf("port after restart = %d, want %d", p, port)
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
