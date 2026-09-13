package orgs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/xid"
	"github.com/tailscale/hujson"

	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/dockerctl"
)

// applyHeadscaleConfigTimeout bounds the background context used for
// applyHeadscaleConfig's write/restart tail (see SetAuthRestrictions/
// SetDNSSettings), once decoupled from the HTTP request's context. It
// must comfortably cover WriteConfigYAML + ConfigTest + both DB writes +
// RestartHeadscale, which itself now blocks until headscale reports
// healthy again after the restart.
const applyHeadscaleConfigTimeout = 45 * time.Second

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

// applyHeadscaleConfig writes cfg to o's headscale config.yaml (both the
// on-disk file and its orgconfigs row) and restarts headscale so it picks
// it up. extraWrites, if non-nil, runs within the same short transaction
// as the config.yaml row write.
//
// This function is NOT a single big transaction because the headscale restarting
// takes several seconds and that would block all other DB operations.
func (o *OrgService) applyHeadscaleConfig(ctx context.Context, cfg *dockerctl.HeadscaleFileConfig, extraWrites func(ctx context.Context, txQueries *sqlcgen.Queries) error) (err error) {
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

	if err := o.persistConfigAndMarkUpdatePending(ctx, updated, extraWrites); err != nil {
		return err
	}

	if err := o.ctl.RestartHeadscale(ctx, o.subdomain); err != nil {
		return err
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
// upserts o's config.yaml orgconfigs row to updated, and sets its status
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
		Path:           dockerctl.ConfigYAMLPath,
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

const AuthRestrictionsConfigKey = "oidc_restrictions"

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
		if errors.Is(err, sql.ErrNoRows) {
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

	// From here on, content is validated; applyHeadscaleConfig persists
	// it (via extraWrites, atomically with the config.yaml row and the
	// update_pending status transition) and restarts headscale as three
	// separate steps (see its doc comment) -- decouple from ctx (the
	// HTTP request's context) with a bounded timeout so a client-side
	// disconnect/network blip (e.g. the request's own fetch getting
	// aborted by the headscale container restart's brief interface churn)
	// can't abort an already-validated save partway through, forcing the
	// caller to redo it.
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyHeadscaleConfigTimeout)
	defer cancel()

	return o.applyHeadscaleConfig(applyCtx, cfg, func(ctx context.Context, txQueries *sqlcgen.Queries) error {
		_, err := txQueries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
			ID:             xid.New().String(),
			OrganizationID: o.orgID,
			Path:           AuthRestrictionsConfigKey,
			Content:        content,
		})
		return err
	})
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

	// See SetAuthRestrictions: decouple the write/restart tail from the
	// HTTP request's context so a client-side network blip can't abort an
	// already-validated save.
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyHeadscaleConfigTimeout)
	defer cancel()

	return o.applyHeadscaleConfig(applyCtx, cfg, nil)
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
