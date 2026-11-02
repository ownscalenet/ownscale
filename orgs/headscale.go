package orgs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/juanfont/headscale/hscontrol"
	hsdns "github.com/juanfont/headscale/hscontrol/dns"
	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/libdns/libdns"
	"github.com/rs/xid"
	"github.com/rs/zerolog"
	"tailscale.com/tailcfg"
	"tailscale.com/types/dnstype"
	"tailscale.com/types/key"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
)

func noisePath(dataDir, subdomain string) string {
	return filepath.Join(dataDir, subdomain, "noise_private.key")
}

const noiseKeyConfigKey = "noise_private.key"

// ensureNoiseKey dumps the key from database to filesystem, or creates it if
// it does not exist in the database.
func (s *Service) ensureNoiseKey(ctx context.Context, orgID, subdomain string) (err error) {
	defer derrors.Wrap(&err, "s.ensureNoiseKey(%q)", subdomain)

	path := noisePath(s.cfg.DataDir, subdomain)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fileKey := strings.TrimSpace(string(file))

	row, err := s.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: orgID,
		Path:           noiseKeyConfigKey,
	})
	switch {
	case err == nil:
		if fileKey == row.Content {
			return nil
		}
		if fileKey != "" {
			s.logger.Warn("noise key file differs from the stored key, restoring the stored one", "subdomain", subdomain)
		}
		return os.WriteFile(path, []byte(row.Content), 0o600)
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}

	stored := fileKey
	if stored == "" {
		text, err := key.NewMachine().MarshalText()
		if err != nil {
			return err
		}
		stored = string(text)
	} else if err := new(key.MachinePrivate).UnmarshalText([]byte(stored)); err != nil {
		return fmt.Errorf("parse noise key file: %w", err)
	}
	if _, err := s.queries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: orgID,
		Path:           noiseKeyConfigKey,
		Content:        stored,
	}); err != nil {
		return err
	}
	if fileKey == "" {
		return os.WriteFile(path, []byte(stored), 0o600)
	}
	return nil
}

// DBFile returns subdomain's headscale database, relative to the data
// directory.
func DBFile(subdomain string) string {
	return filepath.Join(subdomain, "db.sqlite")
}

func sqlitePath(dataDir, subdomain string) string {
	return filepath.Join(dataDir, DBFile(subdomain))
}

// SocketPath returns the unix socket subdomain's headscale instance serves
// its API on, without authentication.
func SocketPath(dataDir, subdomain string) string {
	return filepath.Join(dataDir, subdomain, "headscale.sock")
}

// DefaultACLPolicy is the HuJSON access control policy seeded into every
// new organization's headscale instance. headscale's database policy mode
// starts with no policy; callers shouldn't have to special-case that.
const DefaultACLPolicy = `{
	// Start a line with // to leave a comment.
	// See https://headscale.net/stable/ref/acls/ for the full HuJSON syntax.
	"acls": [
		{
			"action": "accept",
			"src":    ["*"],
			"dst":    ["*:*"],
		},
	],
	// The target machines must enable ssh with either:
	// tailscale up --ssh {API_KEY}
	// tailscale set --ssh
	"ssh": [
		{
			"action": "accept",
			"src":    ["autogroup:member"],
			"dst":    ["autogroup:self"],
			"users":  ["autogroup:nonroot", "root"],
		},
	],
}
`

// headscaleParams is what createHeadscale needs to build an organization's
// headscale instance.
type headscaleParams struct {
	dataDir    string
	subdomain  string
	baseDomain string
	port       int
	// hsCfg is ownscale's stored config; hsCfg.DNS.BaseDomain is the org's
	// MagicDNS domain.
	hsCfg   *HeadscaleConfig
	derpMap *tailcfg.DERPMap
	// certs is nil when node certificates are disabled.
	certs *nodeCerts
	// fast is config.Config.FastOrgs.
	fast bool
}

// nodeCerts configures the DNS provider publishing ACME DNS-01 challenges
// for node certificates.
type nodeCerts struct {
	provider           hsdns.Provider
	providerName       string
	zone               string
	propagationTimeout time.Duration
}

// challengeResolvers find a zone's nameservers when waiting for a challenge
// record to propagate.
var challengeResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// createHeadscale creates an *hscontrol.Headscale instance for p.subdomain,
// listening on 127.0.0.1:p.port. DNS and OIDC settings come from p.hsCfg,
// ownscale's stored config. The org data directory (dataDir/subdomain) is
// created if absent.
func createHeadscale(p headscaleParams) (_ *hscontrol.Headscale, err error) {
	defer derrors.Wrap(&err, "createHeadscale(%q)", p.subdomain)

	dataDir, subdomain, hsCfg := p.dataDir, p.subdomain, p.hsCfg
	pv4 := netip.MustParsePrefix("100.64.0.0/10")
	pv6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")

	// Node writes block until the next tick of this timeout.
	nodeStoreBatchTimeout := 500 * time.Millisecond
	if p.fast {
		nodeStoreBatchTimeout = time.Millisecond
	}

	orgDir := filepath.Dir(noisePath(dataDir, subdomain))
	if err := os.Mkdir(orgDir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}

	dns := ownscaleDNSToTypes(hsCfg.DNS)
	var challenge types.DNSChallengeConfig
	// headscale only issues certificates for MagicDNS names.
	if p.certs != nil && dns.MagicDNS {
		dns.Certificates = types.CertificatesConfig{Enabled: true, Zone: p.certs.zone}
		challenge = types.DNSChallengeConfig{
			PropagationTimeout: p.certs.propagationTimeout,
			Resolvers:          challengeResolvers,
			ProviderName:       p.certs.providerName,
			Provider:           &scopedProvider{Provider: p.certs.provider, domain: dns.BaseDomain},
		}
	}
	cfg := &types.Config{
		ServerURL:           fmt.Sprintf("https://%s.%s", subdomain, p.baseDomain),
		Addr:                fmt.Sprintf("127.0.0.1:%d", p.port),
		NoisePrivateKeyPath: noisePath(dataDir, subdomain),
		PrefixV4:            &pv4,
		PrefixV6:            &pv6,
		IPAllocation:        types.IPAllocationStrategy("sequential"),
		// ownscale fetches the DERP map and pushes updates (see RunDERPMapUpdater).
		DERP: types.DERPConfig{DERPMap: p.derpMap},
		Database: types.DatabaseConfig{
			Type: "sqlite3",
			Sqlite: types.SqliteConfig{
				Path:          sqlitePath(dataDir, subdomain),
				WriteAheadLog: true,
				// SQLite's default. Backups checkpoint on their own, but with
				// them disabled nothing else would.
				WALAutoCheckPoint: 1000,
			},
		},
		Log:      types.LogConfig{Format: "json", Level: zerolog.InfoLevel},
		Taildrop: types.TaildropConfig{Enabled: true},
		// headscale builds node FQDNs (and MagicDNS names) from this field,
		// not from DNSConfig.BaseDomain.
		BaseDomain: dns.BaseDomain,
		DNSConfig:  dns,
		// TailcfgDNSConfig is the pre-computed tailcfg representation sent
		// to nodes. NewHeadscale does not compute it from DNSConfig — the
		// Viper config loader does. We replicate the conversion here so
		// nodes receive a DNS config (nameservers, MagicDNS) from startup.
		TailcfgDNSConfig:     buildTailcfgDNS(dns),
		DNSChallenge:         challenge,
		UnixSocket:           SocketPath(dataDir, subdomain),
		UnixSocketPermission: 0o770,
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
			// ownscale trusts the email each login provider reports, which
			// ownscale's OIDC provider passes on, verified or not.
			EmailVerifiedRequired: false,
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
			NodeStoreBatchTimeout:          nodeStoreBatchTimeout,
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
		Nameservers:   types.Nameservers{Global: dns.Nameservers.Global, Split: dns.Nameservers.Split},
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
	// headscale uses FallbackResolvers when OverrideLocalDNS is false
	// (the default); OverrideLocalDNS is not part of ownscale's config.
	cfg.FallbackResolvers = toResolvers(dns.Nameservers.Global)
	if len(dns.Nameservers.Split) > 0 {
		cfg.Routes = make(map[string][]*dnstype.Resolver, len(dns.Nameservers.Split))
		for domain, nameservers := range dns.Nameservers.Split {
			cfg.Routes[domain] = toResolvers(nameservers)
		}
	}
	if dns.BaseDomain != "" {
		cfg.Domains = []string{dns.BaseDomain}
	}
	cfg.Domains = append(cfg.Domains, dns.SearchDomains...)
	return cfg
}

func toResolvers(nameservers []string) []*dnstype.Resolver {
	var resolvers []*dnstype.Resolver
	for _, ns := range nameservers {
		resolvers = append(resolvers, &dnstype.Resolver{Addr: ns})
	}
	return resolvers
}

// scopedProvider limits a provider shared by every organization to
// challenge records under one organization's MagicDNS domain.
type scopedProvider struct {
	hsdns.Provider
	domain string
}

var errOutOfScope = errors.New("DNS record outside the organization's domain")

func (p *scopedProvider) check(zone string, recs []libdns.Record) error {
	suffix := "." + strings.TrimSuffix(p.domain, ".") + "."
	for _, r := range recs {
		rr := r.RR()
		name := strings.ToLower(libdns.AbsoluteName(rr.Name, zone))
		if rr.Type != "TXT" || !strings.HasPrefix(name, types.ACMEChallengePrefix) || !strings.HasSuffix(name, suffix) {
			return fmt.Errorf("%w: %s %s", errOutOfScope, rr.Type, name)
		}
	}
	return nil
}

func (p *scopedProvider) AppendRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	if err := p.check(zone, recs); err != nil {
		return nil, err
	}
	return p.Provider.AppendRecords(ctx, zone, recs)
}

func (p *scopedProvider) DeleteRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	if err := p.check(zone, recs); err != nil {
		return nil, err
	}
	return p.Provider.DeleteRecords(ctx, zone, recs)
}
