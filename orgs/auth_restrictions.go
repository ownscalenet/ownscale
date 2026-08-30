package orgs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/rs/xid"
	"github.com/tailscale/hujson"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
)

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

// GetAuthRestrictions returns orgID's stored OIDC login allowlist as
// HuJSON. A missing row falls back to defaultAuthRestrictions rather than
// erroring.
func (s *Service) GetAuthRestrictions(ctx context.Context, orgID string) (_ string, err error) {
	defer derrors.Wrap(&err, "s.GetAuthRestrictions(%q)", orgID)

	cfg, err := s.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: orgID,
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

// SetAuthRestrictions validates content as HuJSON, persists it as orgID's
// OIDC login allowlist (orgconfigs), then patches the equivalent
// domains/groups/users into subdomain's running headscale container and
// restarts it so headscale reloads it. The process is in a transaction.
func (s *Service) SetAuthRestrictions(ctx context.Context, orgID, subdomain, content string) (err error) {
	defer derrors.Wrap(&err, "s.SetAuthRestrictions(%q, %q)", orgID, subdomain)

	val, err := hujson.Parse([]byte(content))
	if err != nil {
		return err
	}
	val.Standardize()
	var r authRestrictions
	if err := json.Unmarshal(val.Pack(), &r); err != nil {
		return err
	}

	txQueries, commit, rollback, err := s.queries.StartTx(ctx)
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	defer rollback()

	if _, err := txQueries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: orgID,
		Path:           AuthRestrictionsConfigKey,
		Content:        content,
	}); err != nil {
		return err
	}

	if err := s.ctl.SetOIDCAllowlist(ctx, subdomain, r.Domains, r.Groups, r.Users); err != nil {
		return err
	}

	if err := commit(); err != nil {
		return fmt.Errorf("commit allowlist: %w", err)
	}
	return nil
}
