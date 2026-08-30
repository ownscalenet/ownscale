package orgs

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
)

// AuthRestrictionsContainerType is the orgconfigs.container_type value used
// for the headscale OIDC allowlist row -- reusing the same generic
// (organization_id, container_type, path) table as the ACL policy (see
// ACLContainerType), just with a symbolic path key rather than a real
// on-disk file path, since these values are patched into config.yaml's
// existing oidc block rather than written to a file of their own (see
// dockerctl.Ctl.SetOIDCAllowlist).
const AuthRestrictionsContainerType = "headscale"

// AuthRestrictionsConfigKey is the orgconfigs.path value for the stored
// OIDC allowlist JSON.
const AuthRestrictionsConfigKey = "oidc_restrictions"

// authRestrictions is the JSON shape persisted in orgconfigs.content for
// AuthRestrictionsConfigKey.
type authRestrictions struct {
	Domains []string `json:"domains"`
	Groups  []string `json:"groups"`
	Users   []string `json:"users"`
}

// GetAuthRestrictions returns orgID's stored OIDC login allowlist
// (domains, groups, users). A missing row (no restrictions configured
// yet, e.g. a brand new organization, or one provisioned before this
// feature existed) returns three empty slices rather than an error,
// matching headscale's own "keys absent" default of allowing every
// OIDC-authenticated user.
func (s *Service) GetAuthRestrictions(ctx context.Context, orgID string) (domains, groups, users []string, err error) {
	defer derrors.Wrap(&err, "s.GetAuthRestrictions(%q)", orgID)

	cfg, err := s.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: orgID,
		ContainerType:  AuthRestrictionsContainerType,
		Path:           AuthRestrictionsConfigKey,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, err
	}

	var r authRestrictions
	if err := json.Unmarshal([]byte(cfg.Content), &r); err != nil {
		return nil, nil, nil, err
	}
	return r.Domains, r.Groups, r.Users, nil
}

// SetAuthRestrictions persists orgID's OIDC login allowlist (domains,
// groups, users), then patches it into subdomain's running headscale
// container and restarts it so headscale reloads it (it only reads its
// config file at startup). Persisting to the DB happens before touching
// Docker, mirroring SetACLPolicy, so a Docker-side failure never leaves
// the stored allowlist out of sync with what an admin just saved.
func (s *Service) SetAuthRestrictions(ctx context.Context, orgID, subdomain string, domains, groups, users []string) (err error) {
	defer derrors.Wrap(&err, "s.SetAuthRestrictions(%q, %q)", orgID, subdomain)

	content, err := json.Marshal(authRestrictions{Domains: domains, Groups: groups, Users: users})
	if err != nil {
		return err
	}

	if _, err := s.queries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: orgID,
		ContainerType:  AuthRestrictionsContainerType,
		Path:           AuthRestrictionsConfigKey,
		Content:        string(content),
	}); err != nil {
		return err
	}

	if err := s.ctl.SetOIDCAllowlist(ctx, subdomain, domains, groups, users); err != nil {
		return err
	}
	return nil
}
