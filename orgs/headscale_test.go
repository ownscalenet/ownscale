package orgs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rs/xid"
	"tailscale.com/tailcfg"
	"tailscale.com/types/dnstype"
	"tailscale.com/types/key"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

func TestBuildTailcfgDNS(t *testing.T) {
	tests := []struct {
		name string
		dns  DNSConfig
		want *tailcfg.DNSConfig
	}{
		{
			name: "global only",
			dns: DNSConfig{
				MagicDNS:      true,
				BaseDomain:    "acme.ownscale",
				Nameservers:   Nameservers{Global: []string{"1.1.1.1"}},
				SearchDomains: []string{"internal.example.com"},
			},
			want: &tailcfg.DNSConfig{
				Proxied:           true,
				FallbackResolvers: []*dnstype.Resolver{{Addr: "1.1.1.1"}},
				Domains:           []string{"acme.ownscale", "internal.example.com"},
				ExtraRecords:      []tailcfg.DNSRecord{},
			},
		},
		{
			name: "split",
			dns: DNSConfig{
				MagicDNS:   true,
				BaseDomain: "acme.ownscale",
				Nameservers: Nameservers{
					Global: []string{"1.1.1.1"},
					Split: map[string][]string{
						"corp.example.com": {"10.0.0.53", "10.0.0.54"},
						"lab.example.com":  {"https://dns.example.com/dns-query"},
					},
				},
			},
			want: &tailcfg.DNSConfig{
				Proxied:           true,
				FallbackResolvers: []*dnstype.Resolver{{Addr: "1.1.1.1"}},
				Routes: map[string][]*dnstype.Resolver{
					"corp.example.com": {{Addr: "10.0.0.53"}, {Addr: "10.0.0.54"}},
					"lab.example.com":  {{Addr: "https://dns.example.com/dns-query"}},
				},
				Domains:      []string{"acme.ownscale"},
				ExtraRecords: []tailcfg.DNSRecord{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTailcfgDNS(ownscaleDNSToTypes(tt.dns))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildTailcfgDNS() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func newNoiseKey(t *testing.T) string {
	t.Helper()
	text, err := key.NewMachine().MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func TestEnsureNoiseKey(t *testing.T) {
	stored, other := newNoiseKey(t), newNoiseKey(t)
	tests := []struct {
		name     string
		stored   string // "" means no key in the database
		file     string // "" means no key file
		want     string // "" means any valid key, the same in both places
		wantFail bool
	}{
		{name: "lost file is restored", stored: stored, want: stored},
		{name: "stored key wins over a different file", stored: stored, file: other, want: stored},
		{name: "existing file is backfilled", file: other, want: other},
		{name: "new key when there is none"},
		{name: "corrupt file", file: "garbage", wantFail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			org := dbtest.CreateOrg(t, svc.queries.Queries, "acme")
			path := noisePath(svc.cfg.DataDir, "acme")
			if tt.stored != "" {
				if _, err := svc.queries.UpsertOrgConfig(t.Context(), sqlcgen.UpsertOrgConfigParams{
					ID: xid.New().String(), OrganizationID: org.ID, Path: noiseKeyConfigKey, Content: tt.stored,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if tt.file != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tt.file+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}

			err := svc.ensureNoiseKey(t.Context(), org.ID, "acme")
			if tt.wantFail {
				if err == nil {
					t.Fatal("ensureNoiseKey: want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ensureNoiseKey: %v", err)
			}

			row, err := svc.queries.GetOrgConfig(t.Context(), sqlcgen.GetOrgConfigParams{OrganizationID: org.ID, Path: noiseKeyConfigKey})
			if err != nil {
				t.Fatalf("GetOrgConfig: %v", err)
			}
			var storedKey key.MachinePrivate
			if err := storedKey.UnmarshalText([]byte(row.Content)); err != nil {
				t.Fatalf("stored key %q: %v", row.Content, err)
			}
			if tt.want != "" && row.Content != tt.want {
				t.Errorf("stored key = %q, want %q", row.Content, tt.want)
			}
			file, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fileKey key.MachinePrivate
			if err := fileKey.UnmarshalText([]byte(strings.TrimSpace(string(file)))); err != nil {
				t.Fatalf("key file %q: %v", file, err)
			}
			if !fileKey.Equal(storedKey) {
				t.Errorf("key file %q doesn't hold the stored key %q", file, row.Content)
			}
		})
	}
}

// TestNoiseKeySurvivesLostFile verifies a restarted headscale keeps serving
// the organization's noise key after its key file is lost, so connected
// clients, which never re-fetch the key, can still reach it.
func TestNoiseKeySurvivesLostFile(t *testing.T) {
	svc := newRealService(t)
	createStarted(t, svc, "acme")
	first, _ := svc.running("acme")
	want := first.app.NoisePublicKey()

	if err := os.Remove(noisePath(svc.cfg.DataDir, "acme")); err != nil {
		t.Fatal(err)
	}
	if err := svc.restartHeadscale(t.Context(), "acme"); err != nil {
		t.Fatalf("restartHeadscale: %v", err)
	}

	second, _ := svc.running("acme")
	if got := second.app.NoisePublicKey(); got != want {
		t.Errorf("noise key after restart = %v, want %v", got, want)
	}
}
