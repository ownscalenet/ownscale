package orgs

import (
	"reflect"
	"testing"

	"tailscale.com/tailcfg"
	"tailscale.com/types/dnstype"
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
