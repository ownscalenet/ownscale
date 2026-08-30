package orgs

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rs/xid"
	"github.com/tailscale/hujson"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/dockerctl"
)

// GetACLPolicy returns orgID's stored HuJSON access control policy. Every
// organization gets a default policy row inserted at provisioning time
// (see provision), so a missing row only happens for organizations
// provisioned before this feature existed; those fall back to
// dockerctl.DefaultACLPolicy rather than erroring.
func (s *Service) GetACLPolicy(ctx context.Context, orgID string) (_ string, err error) {
	defer derrors.Wrap(&err, "s.GetACLPolicy(%q)", orgID)

	cfg, err := s.queries.GetOrgConfig(ctx, sqlcgen.GetOrgConfigParams{
		OrganizationID: orgID,
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

// SetACLPolicy validates content as HuJSON, persists it as orgID's ACL
// policy (orgconfigs), uploads it into subdomain's running headscale
// container, and restarts that container so headscale reloads it (it only
// reads its policy file at startup). The DB write happens inside a
// transaction that's only committed once the Docker steps succeed; if
// either fails, the DB write is rolled back so the stored policy never
// diverges from what's actually running.
func (s *Service) SetACLPolicy(ctx context.Context, orgID, subdomain, content string) (err error) {
	defer derrors.Wrap(&err, "s.SetACLPolicy(%q, %q)", orgID, subdomain)

	if _, err := hujson.Parse([]byte(content)); err != nil {
		return fmt.Errorf("invalid HuJSON policy: %w", err)
	}

	txQueries, commit, rollback, err := s.queries.StartTx(ctx)
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	defer rollback()

	if _, err := txQueries.UpsertOrgConfig(ctx, sqlcgen.UpsertOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: orgID,
		Path:           dockerctl.PolicyConfigPath,
		Content:        content,
	}); err != nil {
		return fmt.Errorf("save policy: %w", err)
	}

	if err := s.ctl.WriteACLPolicy(ctx, subdomain, content); err != nil {
		return fmt.Errorf("upload policy: %w", err)
	}
	if err := s.ctl.RestartHeadscale(ctx, subdomain); err != nil {
		return fmt.Errorf("restart headscale: %w", err)
	}

	if err := commit(); err != nil {
		return fmt.Errorf("commit policy: %w", err)
	}
	return nil
}
