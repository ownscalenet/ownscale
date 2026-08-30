package orgs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/xid"
	"github.com/tailscale/hujson"

	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/dockerctl"
)

// OrgService holds methods for operating with a single organization.
type OrgService struct {
	queries   *db.TxQueries
	ctl       *dockerctl.Ctl
	orgID     string
	subdomain string
}

func (o *OrgService) getHeadscaleConfig(ctx context.Context) (_ *dockerctl.HeadscaleFileConfig, err error) {
	defer derrors.Wrap(&err, "o.getHeadscaleConfig(%q)", o.orgID)

	row, err := o.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: o.orgID,
		Path:           dockerctl.ConfigYAMLPath,
	})
	if err != nil {
		return nil, err
	}
	cfg, err := dockerctl.ParseHeadscaleFileConfig(row.Content)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (o *OrgService) applyHeadscaleConfig(ctx context.Context, txQueries *sqlcgen.Queries, cfg *dockerctl.HeadscaleFileConfig) (err error) {
	defer derrors.Wrap(&err, "o.applyHeadscaleConfig(%q, %q)", o.orgID, o.subdomain)

	updated, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if err := o.ctl.WriteConfigYAML(ctx, o.subdomain, updated); err != nil {
		return err
	}

	if err := o.ctl.ConfigTest(ctx, o.subdomain); err != nil {
		return err
	}

	if _, err := txQueries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: o.orgID,
		Path:           dockerctl.ConfigYAMLPath,
		Content:        updated,
	}); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if err := o.ctl.RestartHeadscale(ctx, o.subdomain); err != nil {
		return err
	}
	return nil
}

// GetACLPolicy returns o's stored HuJSON access control policy. Every
// organization gets a default policy row inserted at provisioning time
// (see Service.provision), so a missing row only happens for organizations
// provisioned before this feature existed; those fall back to
// dockerctl.DefaultACLPolicy rather than erroring.
func (o *OrgService) GetACLPolicy(ctx context.Context) (_ string, err error) {
	defer derrors.Wrap(&err, "o.GetACLPolicy(%q)", o.orgID)

	cfg, err := o.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: o.orgID,
		Path:           dockerctl.PolicyConfigPath,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return dockerctl.DefaultACLPolicy, nil
		}
		return "", err
	}
	return cfg.Content, nil
}

// SetACLPolicy validates content as HuJSON, persists it as o's ACL
// policy (orgconfigs), uploads it into o's running headscale
// container, and restarts that container so headscale reloads it (it only
// reads its policy file at startup). The DB write happens inside a
// transaction that's only committed once the Docker steps succeed; if
// either fails, the DB write is rolled back so the stored policy never
// diverges from what's actually running.
func (o *OrgService) SetACLPolicy(ctx context.Context, content string) (err error) {
	defer derrors.Wrap(&err, "o.SetACLPolicy(%q)", o.orgID)

	if _, err := hujson.Parse([]byte(content)); err != nil {
		return fmt.Errorf("invalid HuJSON policy: %w", err)
	}

	txQueries, commit, rollback, err := o.queries.StartTx(ctx)
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	defer rollback()

	if _, err := txQueries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: o.orgID,
		Path:           dockerctl.PolicyConfigPath,
		Content:        content,
	}); err != nil {
		return fmt.Errorf("save policy: %w", err)
	}

	if err := o.ctl.WriteACLPolicy(ctx, o.subdomain, content); err != nil {
		return fmt.Errorf("upload policy: %w", err)
	}
	if err := o.ctl.RestartHeadscale(ctx, o.subdomain); err != nil {
		return fmt.Errorf("restart headscale: %w", err)
	}

	if err := commit(); err != nil {
		return fmt.Errorf("commit policy: %w", err)
	}
	return nil
}

const AuthRestrictionsConfigKey = "oidc_restrictions"

const defaultAuthRestrictions = `{
  // Lines starting with // are comments.
  // Leave comments for future reference or explanations about the settings.
  "domains": [],
  "groups": [],
  "users": [],
}
`

type authRestrictions struct {
	Domains []string `json:"domains"`
	Groups  []string `json:"groups"`
	Users   []string `json:"users"`
}

// GetAuthRestrictions returns o's stored OIDC login allowlist as
// HuJSON. A missing row falls back to defaultAuthRestrictions rather than
// erroring.
func (o *OrgService) GetAuthRestrictions(ctx context.Context) (_ string, err error) {
	defer derrors.Wrap(&err, "o.GetAuthRestrictions(%q)", o.orgID)

	cfg, err := o.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: o.orgID,
		Path:           AuthRestrictionsConfigKey,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return defaultAuthRestrictions, nil
		}
		return "", err
	}
	return cfg.Content, nil
}

// SetAuthRestrictions validates content as HuJSON, persists it as o's
// OIDC login allowlist (orgconfigs), then patches the equivalent
// domains/groups/users into o's config.yaml (both its orgconfigs row
// and its running headscale container) and restarts headscale so
// it reloads it.
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

	txQueries, commit, rollback, err := o.queries.StartTx(ctx)
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	defer rollback()

	if _, err := txQueries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: o.orgID,
		Path:           AuthRestrictionsConfigKey,
		Content:        content,
	}); err != nil {
		return err
	}

	if err := o.applyHeadscaleConfig(ctx, txQueries, cfg); err != nil {
		return err
	}

	if err := commit(); err != nil {
		return fmt.Errorf("commit allowlist: %w", err)
	}
	return nil
}

// GetDNSSettings returns o's live DNS settings, read from its
// config.yaml orgconfigs row (the database row is the single source of
// truth; the file on disk is write-only, never read back by ownscale).
func (o *OrgService) GetDNSSettings(ctx context.Context) (_ dockerctl.DNSConfig, err error) {
	defer derrors.Wrap(&err, "o.GetDNSSettings(%q)", o.orgID)

	cfg, err := o.getHeadscaleConfig(ctx)
	if err != nil {
		return dockerctl.DNSConfig{}, err
	}
	return normalizeDNSConfig(cfg.DNS), nil
}

// SetDNSSettings validates dns, then persists it into o's config.yaml
// (both the orgconfigs row and its running headscale container)
// and restarts headscale so it's applied. Validation happens before any
// write, mirroring SetACLPolicy/SetAuthRestrictions' validate-then-persist
// ordering. Most of what headscale itself would consider invalid (e.g. a
// malformed nameserver or an empty base_domain with magic_dns enabled) is
// instead caught by the headscale-configtest step in applyHeadscaleConfig;
// validateDNSSettings only covers the handful of structural cases
// configtest doesn't catch at all.
func (o *OrgService) SetDNSSettings(ctx context.Context, dns dockerctl.DNSConfig) (err error) {
	defer derrors.Wrap(&err, "o.SetDNSSettings(%q)", o.orgID)

	if err := validateDNSSettings(dns); err != nil {
		return err
	}

	cfg, err := o.getHeadscaleConfig(ctx)
	if err != nil {
		return err
	}
	cfg.DNS = dns

	txQueries, commit, rollback, err := o.queries.StartTx(ctx)
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	defer rollback()

	if err := o.applyHeadscaleConfig(ctx, txQueries, cfg); err != nil {
		return err
	}

	if err := commit(); err != nil {
		return fmt.Errorf("commit dns settings: %w", err)
	}
	return nil
}

// validateDNSSettings runs the minimal structural checks headscale's own
// "headscale configtest" (invoked by applyHeadscaleConfig) doesn't
// perform: it accepts an empty base domain as long as magic_dns is off,
// silently drops malformed nameservers/extra_records rather than
// rejecting them, and doesn't reject duplicate records.
func validateDNSSettings(dns dockerctl.DNSConfig) error {
	if strings.TrimSpace(dns.BaseDomain) == "" {
		return dockerctl.NewInvalidConfigError("tailnet name must not be empty")
	}

	seen := make(map[[2]string]bool, len(dns.ExtraRecords))
	for _, r := range dns.ExtraRecords {
		if strings.TrimSpace(r.Name) == "" {
			return dockerctl.NewInvalidConfigError("DNS record name must not be empty")
		}
		if strings.TrimSpace(r.Value) == "" {
			return dockerctl.NewInvalidConfigError("DNS record value must not be empty for " + r.Name)
		}
		typ := strings.ToUpper(r.Type)
		key := [2]string{r.Name, typ}
		if seen[key] {
			return dockerctl.NewInvalidConfigError("duplicate DNS record " + r.Name + " of type " + typ)
		}
		seen[key] = true
	}

	return nil
}

// normalizeDNSConfig fills in empty slices for any nil slice field so the
// JSON response always has arrays (never null) for the frontend to
// .map/.length over. Headscale's own config.yaml omits empty dns keys
// (see the omitempty yaml tags on DNSConfig/Nameservers), so a freshly
// parsed config routinely has nil ExtraRecords/SearchDomains/
// Nameservers.Global.
func normalizeDNSConfig(dns dockerctl.DNSConfig) dockerctl.DNSConfig {
	if dns.Nameservers.Global == nil {
		dns.Nameservers.Global = []string{}
	}
	if dns.SearchDomains == nil {
		dns.SearchDomains = []string{}
	}
	if dns.ExtraRecords == nil {
		dns.ExtraRecords = []dockerctl.DNSRecord{}
	}
	return dns
}
