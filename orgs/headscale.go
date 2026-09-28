package orgs

import (
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/juanfont/headscale/hscontrol"
	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/rs/zerolog"
	"tailscale.com/tailcfg"
	"tailscale.com/types/dnstype"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/derrors"
)

func noisePath(dataDir, subdomain string) string {
	return filepath.Join(dataDir, subdomain, "noise_private.key")
}
func sqlitePath(dataDir, subdomain string) string {
	return filepath.Join(dataDir, subdomain, "db.sqlite")
}

// SocketPath returns the unix socket subdomain's headscale instance serves
// its API on, without authentication.
func SocketPath(dataDir, subdomain string) string {
	return filepath.Join(dataDir, subdomain, "headscale.sock")
}

// defaultACLPolicy is the HuJSON access control policy seeded into every
// new organization's headscale instance. headscale's database policy mode
// starts with no policy; callers shouldn't have to special-case that.
const defaultACLPolicy = `{
	// Start a line with // to leave a comment.
	// See https://headscale.net/stable/ref/acls/ for the full HuJSON syntax.
	"acls": [
		{
			"action": "accept",
			"src":    ["*"],
			"dst":    ["*:*"],
		},
	],
}
`

// createHeadscale creates an *hscontrol.Headscale instance for subdomain,
// listening on 127.0.0.1:port. DNS and OIDC settings come from hsCfg,
// ownscale's stored config. The org data directory (dataDir/subdomain) is
// created if absent.
func createHeadscale(
	dataDir, subdomain, baseDomain string,
	port int,
	hsCfg *HeadscaleConfig,
	derpCfg config.DerpConfig,
) (_ *hscontrol.Headscale, err error) {
	defer derrors.Wrap(&err, "createHeadscale(%q)", subdomain)

	pv4 := netip.MustParsePrefix("100.64.0.0/10")
	pv6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")

	orgDir := filepath.Dir(noisePath(dataDir, subdomain))
	if err := os.Mkdir(orgDir, 0755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}

	ts, err := url.Parse("https://controlplane.tailscale.com/derpmap/default")
	if err != nil {
		return nil, err
	}
	// Always include Tailscale's default DERP servers so nodes can relay
	// even when a custom DERP server is temporarily unreachable.
	derp := types.DERPConfig{
		URLs:            []url.URL{*ts},
		AutoUpdate:      true,
		UpdateFrequency: time.Hour * 3,
	}
	if derpCfg.Enabled() {
		region := derpCfg.Region()
		derp.DERPMap = &tailcfg.DERPMap{Regions: map[tailcfg.DERPRegionID]*tailcfg.DERPRegion{region.RegionID: region}}
	}

	dns := ownscaleDNSToTypes(hsCfg.DNS)
	cfg := &types.Config{
		ServerURL:           fmt.Sprintf("https://%s.%s", subdomain, baseDomain),
		Addr:                fmt.Sprintf("127.0.0.1:%d", port),
		NoisePrivateKeyPath: noisePath(dataDir, subdomain),
		PrefixV4:            &pv4,
		PrefixV6:            &pv6,
		IPAllocation:        types.IPAllocationStrategy("sequential"),
		DERP:                derp,
		Database: types.DatabaseConfig{
			Type: "sqlite3",
			Sqlite: types.SqliteConfig{
				Path:          sqlitePath(dataDir, subdomain),
				WriteAheadLog: true,
			},
		},
		Log:       types.LogConfig{Format: "json", Level: zerolog.InfoLevel},
		Taildrop:  types.TaildropConfig{Enabled: true},
		DNSConfig: dns,
		// TailcfgDNSConfig is the pre-computed tailcfg representation sent
		// to nodes. NewHeadscale does not compute it from DNSConfig — the
		// Viper config loader does. We replicate the conversion here so
		// nodes receive a DNS config (nameservers, MagicDNS) from startup.
		TailcfgDNSConfig:     buildTailcfgDNS(dns),
		UnixSocket:           SocketPath(dataDir, subdomain),
		UnixSocketPermission: 0770,
		Policy:               types.PolicyConfig{Mode: types.PolicyMode("database")},
		Node: types.NodeConfig{
			Ephemeral: types.EphemeralConfig{
				InactivityTimeout: time.Second * 120,
			},
			Routes: types.RouteConfig{HA: types.HARouteConfig{
				ProbeInterval: 10 * time.Second,
				ProbeTimeout:  5 * time.Second,
			}},
		},
		OIDC: types.OIDCConfig{
			Issuer:         hsCfg.OIDC.Issuer,
			ClientID:       hsCfg.OIDC.ClientID,
			ClientSecret:   hsCfg.OIDC.ClientSecret,
			Scope:          []string{"openid", "profile", "email", "offline_access"},
			AllowedDomains: hsCfg.OIDC.AllowedDomains,
			AllowedGroups:  hsCfg.OIDC.AllowedGroups,
			AllowedUsers:   hsCfg.OIDC.AllowedUsers,
			PKCE: types.PKCEConfig{
				Enabled: true,
				Method:  "S256",
			},
		},
		Tuning: types.Tuning{
			BatchChangeDelay:               800 * time.Millisecond,
			BatcherWorkers:                 types.DefaultBatcherWorkers(),
			NotifierSendTimeout:            800 * time.Millisecond,
			NodeMapSessionBufferedChanSize: 30,
			NodeStoreBatchSize:             100,
			NodeStoreBatchTimeout:          500 * time.Millisecond,
		},
	}

	return hscontrol.NewHeadscale(cfg)
}

// ownscaleDNSToTypes converts the ownscale DNS config representation to
// headscale's types.DNSConfig so it can be passed to buildTailcfgDNS.
func ownscaleDNSToTypes(dns DNSConfig) types.DNSConfig {
	records := make([]tailcfg.DNSRecord, len(dns.ExtraRecords))
	for i, r := range dns.ExtraRecords {
		records[i] = tailcfg.DNSRecord{Name: r.Name, Type: r.Type, Value: r.Value}
	}
	return types.DNSConfig{
		MagicDNS:      dns.MagicDNS,
		BaseDomain:    dns.BaseDomain,
		Nameservers:   types.Nameservers{Global: dns.Nameservers.Global},
		SearchDomains: dns.SearchDomains,
		ExtraRecords:  records,
	}
}

// buildTailcfgDNS converts headscale's DNSConfig to the tailcfg.DNSConfig
// that the mapper sends to nodes. headscale's Viper config loader does this
// automatically; when building types.Config directly (as createHeadscale
// does) it must be done explicitly.
func buildTailcfgDNS(dns types.DNSConfig) *tailcfg.DNSConfig {
	cfg := &tailcfg.DNSConfig{
		Proxied:      dns.MagicDNS,
		ExtraRecords: dns.ExtraRecords,
	}
	var resolvers []*dnstype.Resolver
	for _, ns := range dns.Nameservers.Global {
		resolvers = append(resolvers, &dnstype.Resolver{Addr: ns})
	}
	// headscale uses FallbackResolvers when OverrideLocalDNS is false
	// (the default); OverrideLocalDNS is not part of ownscale's config.
	cfg.FallbackResolvers = resolvers
	if dns.BaseDomain != "" {
		cfg.Domains = []string{dns.BaseDomain}
	}
	cfg.Domains = append(cfg.Domains, dns.SearchDomains...)
	return cfg
}
