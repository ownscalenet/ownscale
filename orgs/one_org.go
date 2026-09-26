package orgs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ownscalenet/headscale/hscontrol"
	"github.com/ownscalenet/headscale/hscontrol/types/change"
	"github.com/rs/xid"
	"github.com/tailscale/hujson"

	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
)

// applyHeadscaleConfigTimeout bounds the context timeout used when
// writing and applying a new headscale config.
const applyHeadscaleConfigTimeout = 45 * time.Second

// OrgService holds methods for operating on a single organization.
type OrgService struct {
	queries      *db.TxQueries
	orgID        string
	subdomain    string
	getHeadscale func() (*hscontrol.Headscale, bool)
}

func (o *OrgService) getHeadscaleConfig(ctx context.Context) (_ *HeadscaleFileConfig, err error) {
	defer derrors.Wrap(&err, "o.getHeadscaleConfig(%q)", o.orgID)

	row, err := o.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: o.orgID,
		Path:           ConfigYAMLPath,
	})
	if err != nil {
		return nil, err
	}
	cfg, err := ParseHeadscaleFileConfig(row.Content)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyHeadscaleConfig persists cfg to the orgconfigs row, marks the
// org update_pending, and restarts its headscale instance so the new
// config takes effect. extraWrites runs within the same short transaction
// as the config row write.
//
// This function is NOT a single big transaction because headscale
// restarting takes several seconds and that would block all other DB ops.
func (o *OrgService) applyHeadscaleConfig(ctx context.Context, cfg *HeadscaleFileConfig, extraWrites func(ctx context.Context, txQueries *sqlcgen.Queries) error) (err error) {
	defer derrors.Wrap(&err, "o.applyHeadscaleConfig(%q, %q)", o.orgID, o.subdomain)

	updated, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if err := validateConfigYAML(updated); err != nil {
		return err
	}

	if err := o.persistConfigAndMarkUpdatePending(ctx, updated, extraWrites); err != nil {
		return err
	}

	if o.getHeadscale != nil {
		if app, ok := o.getHeadscale(); ok {
			app.GetState().DebugConfig().TailcfgDNSConfig = buildTailcfgDNS(ownscaleDNSToTypes(cfg.DNS))
			app.Change(change.DNSConfig())
		}
	}

	if _, err := o.queries.UpdateOrganizationStatus(ctx, sqlcgen.UpdateOrganizationStatusParams{
		ID:     o.orgID,
		Status: StatusActive,
	}); err != nil {
		return fmt.Errorf("mark organization active: %w", err)
	}
	return nil
}

// validateConfigYAML parses content and validates the DNS block.
// Replaces the previous `headscale configtest` exec approach.
func validateConfigYAML(content string) error {
	cfg, err := ParseHeadscaleFileConfig(content)
	if err != nil {
		return err
	}
	return validateDNSSettings(cfg.DNS)
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
		Path:           ConfigYAMLPath,
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

// GetAuthRestrictions returns o's stored OIDC login allowlist as HuJSON.
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
// domains/groups/users into o's config.yaml and restarts headscale.
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
			Path:           AuthRestrictionsConfigKey,
			Content:        content,
		})
		return err
	})
}

// GetDNSSettings returns o's live DNS settings from its config.yaml row.
func (o *OrgService) GetDNSSettings(ctx context.Context) (_ DNSConfig, err error) {
	defer derrors.Wrap(&err, "o.GetDNSSettings(%q)", o.orgID)

	cfg, err := o.getHeadscaleConfig(ctx)
	if err != nil {
		return DNSConfig{}, err
	}
	return normalizeDNSConfig(cfg.DNS), nil
}

// SetDNSSettings validates dns, persists it into o's config.yaml row,
// and restarts headscale so it's applied.
func (o *OrgService) SetDNSSettings(ctx context.Context, dns DNSConfig) (err error) {
	defer derrors.Wrap(&err, "o.SetDNSSettings(%q)", o.orgID)

	if err := validateDNSSettings(dns); err != nil {
		return err
	}

	cfg, err := o.getHeadscaleConfig(ctx)
	if err != nil {
		return err
	}
	cfg.DNS = dns

	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyHeadscaleConfigTimeout)
	defer cancel()

	return o.applyHeadscaleConfig(applyCtx, cfg, nil)
}

// validateDNSSettings runs structural checks that headscale's configtest
// doesn't catch: empty base domain, empty record names/values, duplicates.
func validateDNSSettings(dns DNSConfig) error {
	if strings.TrimSpace(dns.BaseDomain) == "" {
		return NewInvalidConfigError("tailnet name must not be empty")
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

// normalizeDNSConfig fills in empty slices for nil slice fields so the
// JSON response always has arrays (never null) for the frontend.
func normalizeDNSConfig(dns DNSConfig) DNSConfig {
	if dns.Nameservers.Global == nil {
		dns.Nameservers.Global = []string{}
	}
	if dns.SearchDomains == nil {
		dns.SearchDomains = []string{}
	}
	if dns.ExtraRecords == nil {
		dns.ExtraRecords = []DNSRecord{}
	}
	return dns
}
