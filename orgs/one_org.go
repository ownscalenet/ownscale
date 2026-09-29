package orgs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/rs/xid"
	"github.com/tailscale/hujson"
	"tailscale.com/util/dnsname"

	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
)

// applyHeadscaleConfigTimeout bounds the context timeout used when
// writing and applying a new headscale config.
const applyHeadscaleConfigTimeout = 45 * time.Second

// OrgService holds methods for operating on a single organization.
type OrgService struct {
	queries   *db.TxQueries
	orgID     string
	subdomain string
	// restart restarts the org's headscale so it picks up its stored config.
	restart func(ctx context.Context) error
}

func (o *OrgService) getHeadscaleConfig(ctx context.Context) (_ *HeadscaleConfig, err error) {
	defer derrors.Wrap(&err, "o.getHeadscaleConfig(%q)", o.orgID)

	row, err := o.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: o.orgID,
		Path:           HeadscaleConfigKey,
	})
	if err != nil {
		return nil, err
	}
	cfg, err := parseHeadscaleConfig(row.Content)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyHeadscaleConfig persists cfg to the orgconfigs row, marks the
// org update_pending, restarts its headscale instance so the new config
// takes effect, then marks the org active again. If the restart fails the
// org stays update_pending, and startup recovery retries it. extraWrites
// runs within the same short transaction as the config row write.
//
// This function is NOT a single big transaction because headscale
// restarting takes several seconds and that would block all other DB ops.
func (o *OrgService) applyHeadscaleConfig(ctx context.Context, cfg *HeadscaleConfig, extraWrites func(ctx context.Context, txQueries *sqlcgen.Queries) error) (err error) {
	defer derrors.Wrap(&err, "o.applyHeadscaleConfig(%q, %q)", o.orgID, o.subdomain)

	if err := validateDNSSettings(cfg.DNS); err != nil {
		return err
	}
	updated, err := cfg.Marshal()
	if err != nil {
		return err
	}

	if err := o.persistConfigAndMarkUpdatePending(ctx, updated, extraWrites); err != nil {
		return err
	}

	if err := o.restart(ctx); err != nil {
		return fmt.Errorf("restart headscale (organization stays %s): %w", StatusUpdatePending, err)
	}

	if _, err := o.queries.UpdateOrganizationStatus(ctx, sqlcgen.UpdateOrganizationStatusParams{
		ID:     o.orgID,
		Status: StatusActive,
	}); err != nil {
		return fmt.Errorf("mark organization active: %w", err)
	}
	return nil
}

// persistConfigAndMarkUpdatePending runs extraWrites (if non-nil),
// upserts o's HeadscaleConfigKey orgconfigs row to updated, and sets its status
// to StatusUpdatePending.
func (o *OrgService) persistConfigAndMarkUpdatePending(
	ctx context.Context,
	updated string,
	extraWrites func(ctx context.Context, txQueries *sqlcgen.Queries) error,
) (err error) {
	defer derrors.Wrap(&err, "o.persistConfigAndMarkUpdatePending(%q)", o.orgID)

	txQueries, commit, rollback, err := o.queries.StartTx(ctx)
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	defer rollback()

	if extraWrites != nil {
		if err := extraWrites(ctx, txQueries); err != nil {
			return err
		}
	}

	if _, err := txQueries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: o.orgID,
		Path:           HeadscaleConfigKey,
		Content:        updated,
	}); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if _, err := txQueries.UpdateOrganizationStatus(ctx, sqlcgen.UpdateOrganizationStatusParams{
		ID:     o.orgID,
		Status: StatusUpdatePending,
	}); err != nil {
		return fmt.Errorf("mark organization update-pending: %w", err)
	}

	if err := commit(); err != nil {
		return fmt.Errorf("commit config: %w", err)
	}
	return nil
}

// authRestrictionsConfigKey is the orgconfigs key under which the HuJSON
// OIDC login allowlist is stored as the user wrote it.
const authRestrictionsConfigKey = "oidc_restrictions"

const defaultAuthRestrictions = `{
	// Start a line with // to leave a comment.
	"domains": [],
	"groups":  [],
	"users":   [],
}

`

type authRestrictions struct {
	Domains []string `json:"domains"`
	Groups  []string `json:"groups"`
	Users   []string `json:"users"`
}

// GetAuthRestrictions returns o's stored OIDC login allowlist as HuJSON.
func (o *OrgService) GetAuthRestrictions(ctx context.Context) (_ string, err error) {
	defer derrors.Wrap(&err, "o.GetAuthRestrictions(%q)", o.orgID)

	cfg, err := o.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: o.orgID,
		Path:           authRestrictionsConfigKey,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return defaultAuthRestrictions, nil
		}
		return "", err
	}
	return cfg.Content, nil
}

// SetAuthRestrictions validates content as HuJSON, persists it as o's
// OIDC login allowlist (orgconfigs), then patches the equivalent
// domains/groups/users into o's HeadscaleConfig and restarts headscale.
func (o *OrgService) SetAuthRestrictions(ctx context.Context, content string) (err error) {
	defer derrors.Wrap(&err, "o.SetAuthRestrictions(%q)", o.orgID)

	val, err := hujson.Parse([]byte(content))
	if err != nil {
		return err
	}
	val.Standardize()
	var r authRestrictions
	if err := json.Unmarshal(val.Pack(), &r); err != nil {
		return err
	}

	cfg, err := o.getHeadscaleConfig(ctx)
	if err != nil {
		return err
	}
	cfg.OIDC.AllowedDomains = r.Domains
	cfg.OIDC.AllowedGroups = r.Groups
	cfg.OIDC.AllowedUsers = r.Users

	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyHeadscaleConfigTimeout)
	defer cancel()

	return o.applyHeadscaleConfig(applyCtx, cfg, func(ctx context.Context, txQueries *sqlcgen.Queries) error {
		_, err := txQueries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
			ID:             xid.New().String(),
			OrganizationID: o.orgID,
			Path:           authRestrictionsConfigKey,
			Content:        content,
		})
		return err
	})
}

// GetDNSSettings returns o's stored DNS settings.
func (o *OrgService) GetDNSSettings(ctx context.Context) (_ DNSConfig, err error) {
	defer derrors.Wrap(&err, "o.GetDNSSettings(%q)", o.orgID)

	cfg, err := o.getHeadscaleConfig(ctx)
	if err != nil {
		return DNSConfig{}, err
	}
	return normalizeDNSConfig(cfg.DNS), nil
}

// SetDNSSettings validates dns, persists it into o's HeadscaleConfig,
// and restarts headscale so it's applied.
func (o *OrgService) SetDNSSettings(ctx context.Context, dns DNSConfig) (err error) {
	defer derrors.Wrap(&err, "o.SetDNSSettings(%q)", o.orgID)

	if err := validateDNSSettings(dns); err != nil {
		return err
	}
	split, err := canonicalSplitDNS(dns.Nameservers.Split, dns.BaseDomain)
	if err != nil {
		return err
	}
	dns.Nameservers.Split = split

	cfg, err := o.getHeadscaleConfig(ctx)
	if err != nil {
		return err
	}
	cfg.DNS = dns

	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyHeadscaleConfigTimeout)
	defer cancel()

	return o.applyHeadscaleConfig(applyCtx, cfg, nil)
}

// validateDNSSettings rejects an empty base domain, invalid nameservers,
// empty record names/values, and duplicate records.
func validateDNSSettings(dns DNSConfig) error {
	if strings.TrimSpace(dns.BaseDomain) == "" {
		return NewInvalidConfigError("tailnet name must not be empty")
	}

	for _, ns := range dns.Nameservers.Global {
		if !validNameserver(ns) {
			return NewInvalidConfigError(fmt.Sprintf("nameserver %q must be an IP address or an https:// DNS-over-HTTPS URL", ns))
		}
	}
	for domain, nameservers := range dns.Nameservers.Split {
		if len(nameservers) == 0 {
			return NewInvalidConfigError(fmt.Sprintf("split DNS domain %q needs at least one nameserver", domain))
		}
		for _, ns := range nameservers {
			if !validNameserver(ns) {
				return NewInvalidConfigError(fmt.Sprintf("nameserver %q for %q must be an IP address or an https:// DNS-over-HTTPS URL", ns, domain))
			}
		}
	}

	seen := make(map[[2]string]bool, len(dns.ExtraRecords))
	for _, r := range dns.ExtraRecords {
		if strings.TrimSpace(r.Name) == "" {
			return NewInvalidConfigError("DNS record name must not be empty")
		}
		if strings.TrimSpace(r.Value) == "" {
			return NewInvalidConfigError("DNS record value must not be empty for " + r.Name)
		}
		typ := strings.ToUpper(r.Type)
		key := [2]string{r.Name, typ}
		if seen[key] {
			return NewInvalidConfigError("duplicate DNS record " + r.Name + " of type " + typ)
		}
		seen[key] = true
	}

	return nil
}

// validNameserver reports whether ns is a resolver headscale passes on to
// nodes; it silently drops anything else, so reject it up front instead.
func validNameserver(ns string) bool {
	if _, err := netip.ParseAddr(ns); err == nil {
		return true
	}
	u, err := url.Parse(ns)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// canonicalSplitDNS returns split with each domain lowercased and without
// a trailing dot, so equivalent spellings can't become separate entries.
// It rejects invalid domains and ones that would shadow MagicDNS names
// under baseDomain.
func canonicalSplitDNS(split map[string][]string, baseDomain string) (map[string][]string, error) {
	base, err := dnsname.ToFQDN(strings.ToLower(strings.TrimSpace(baseDomain)))
	if err != nil {
		return nil, NewInvalidConfigError(fmt.Sprintf("invalid tailnet name %q", baseDomain))
	}

	out := make(map[string][]string, len(split))
	for domain, nameservers := range split {
		fqdn, ok := parseDomain(domain)
		if !ok {
			return nil, NewInvalidConfigError(fmt.Sprintf("invalid split DNS domain %q", domain))
		}
		if base.Contains(fqdn) {
			return nil, NewInvalidConfigError(fmt.Sprintf("split DNS domain %q is inside the tailnet name %q", domain, baseDomain))
		}
		key := fqdn.WithoutTrailingDot()
		if _, ok := out[key]; ok {
			return nil, NewInvalidConfigError(fmt.Sprintf("duplicate split DNS domain %q", key))
		}
		out[key] = nameservers
	}
	return out, nil
}

// parseDomain lowercases s and reports whether it's a non-root domain made
// of hostname-style labels.
func parseDomain(s string) (dnsname.FQDN, bool) {
	fqdn, err := dnsname.ToFQDN(strings.ToLower(strings.TrimSpace(s)))
	if err != nil || fqdn.NumLabels() == 0 {
		return "", false
	}
	for label := range strings.SplitSeq(fqdn.WithoutTrailingDot(), ".") {
		if dnsname.ValidLabel(label) != nil {
			return "", false
		}
	}
	return fqdn, true
}

// normalizeDNSConfig fills in empty slices for nil slice fields so the
// JSON response always has arrays (never null) for the frontend.
func normalizeDNSConfig(dns DNSConfig) DNSConfig {
	if dns.Nameservers.Global == nil {
		dns.Nameservers.Global = []string{}
	}
	if dns.Nameservers.Split == nil {
		dns.Nameservers.Split = map[string][]string{}
	}
	if dns.SearchDomains == nil {
		dns.SearchDomains = []string{}
	}
	if dns.ExtraRecords == nil {
		dns.ExtraRecords = []DNSRecord{}
	}
	return dns
}
