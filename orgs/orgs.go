// Package orgs contains organization-provisioning logic shared by the
// Provisioner (background pool of ready-to-assign organizations) and the
// web server (assigning a pooled organization to a first-time login).
package orgs

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/dockerctl"

	"github.com/rs/xid"
	"golang.org/x/crypto/bcrypt"
)

// Service provisions organizations against a fixed set of queries, Docker
// controller, and base domain. It's shared by the CLI (cmd/osa) and the web
// server (handlers) so both wire it up once instead of threading
// the same dependencies through every call site.
type Service struct {
	queries    *db.TxQueries
	ctl        *dockerctl.Ctl
	baseDomain string
	issuer     string
	derpCfg    config.DerpConfig
}

// NewService builds a Service backed by queries and ctl, provisioning
// organizations under baseDomain. derpCfg configures a self-hosted DERP
// relay for every organization's headscale container (see
// config.DerpConfig); its zero value keeps using Tailscale's public DERP
// map, unchanged from before this parameter existed.
func NewService(queries *db.TxQueries, ctl *dockerctl.Ctl, baseDomain, issuer string, derpCfg config.DerpConfig) *Service {
	return &Service{queries: queries, ctl: ctl, baseDomain: baseDomain, issuer: issuer, derpCfg: derpCfg}
}

// BaseDomain returns the domain organizations are provisioned under (e.g.
// "example.com" for "<sub>.example.com").
func (s *Service) BaseDomain() string {
	return s.baseDomain
}

// ForOrg instantiates an OrgService.
func (s *Service) ForOrg(org sqlcgen.Organization) *OrgService {
	return &OrgService{
		queries:   s.queries,
		ctl:       s.ctl,
		orgID:     org.ID,
		subdomain: org.Subdomain,
	}
}

// provisionNew provisions a brand-new organization under subdomain
// end-to-end and returns it already inserted with status successStatus.
func (s *Service) provisionNew(ctx context.Context, subdomain, successStatus string) (_ sqlcgen.Organization, err error) {
	if err := dockerctl.ValidateSubdomain(subdomain); err != nil {
		return sqlcgen.Organization{}, err
	}

	clientID, err := randomAlphaNum(8)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	clientID = "os_" + subdomain + "_" + clientID
	clientSecret, err := randomAlphaNum(32)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	clientSecretHash, err := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	redirectURIsJSON, err := json.Marshal([]string{
		"https://" + subdomain + "." + s.baseDomain + "/oidc/callback",
		"https://" + subdomain + "." + s.baseDomain + "/admin/oidc/callback",
	})
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	oidcCreds := dockerctl.OIDCClientCredentials{
		Issuer:       s.issuer,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
	// This config goes to DB and becomes the authority.
	hsConfig, err := dockerctl.BuildHeadscaleConfig(subdomain, subdomain+"."+s.baseDomain, oidcCreds, s.derpCfg)
	if err != nil {
		return sqlcgen.Organization{}, err
	}

	if _, err := s.ctl.CreateOrganization(ctx, subdomain, s.baseDomain, oidcCreds, s.derpCfg); err != nil {
		// Best effort clean-up.
		if cleanupErr := s.ctl.RemoveOrganization(ctx, subdomain); cleanupErr != nil {
			err = fmt.Errorf("%w (also failed to clean up partially-created container: %w)", err, cleanupErr)
		}
		return sqlcgen.Organization{}, err
	}

	org, err := s.insertProvisioned(ctx, subdomain, successStatus, clientID, string(clientSecretHash), string(redirectURIsJSON), hsConfig)
	if err != nil {
		if cleanupErr := s.ctl.RemoveOrganization(ctx, subdomain); cleanupErr != nil {
			err = fmt.Errorf("%w (also failed to clean up orphaned container: %w)", err, cleanupErr)
		}
		return sqlcgen.Organization{}, err
	}
	return org, nil
}

func (s *Service) insertProvisioned(ctx context.Context, subdomain, status, clientID, clientSecretHash, redirectURIsJSON, hsConfig string) (_ sqlcgen.Organization, err error) {
	txQueries, commit, rollback, err := s.queries.StartTx(ctx)
	if err != nil {
		return sqlcgen.Organization{}, err
	}
	defer rollback()

	org, err := txQueries.CreateOrganization(ctx, sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: subdomain,
		Status:    status,
	})
	if err != nil {
		return sqlcgen.Organization{}, fmt.Errorf("create organization record (already exists?): %w", err)
	}
	if _, err := txQueries.CreateOIDCClient(ctx, sqlcgen.CreateOIDCClientParams{
		ID:               xid.New().String(),
		OrganizationID:   org.ID,
		ClientID:         clientID,
		ClientSecretHash: clientSecretHash,
		RedirectUris:     redirectURIsJSON,
	}); err != nil {
		return sqlcgen.Organization{}, err
	}
	if _, err := txQueries.CreateOrgConfig(ctx, sqlcgen.CreateOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: org.ID,
		Path:           dockerctl.ConfigYAMLPath,
		Content:        hsConfig,
	}); err != nil {
		return sqlcgen.Organization{}, err
	}
	if err := commit(); err != nil {
		return sqlcgen.Organization{}, err
	}
	return org, nil
}

// Create provisions a brand-new organization end-to-end and marks it
// "active" immediately. This func only exists for tests sake.
func (s *Service) Create(ctx context.Context, subdomain string) (_ sqlcgen.Organization, err error) {
	defer derrors.Wrap(&err, "s.Create(%q)", subdomain)
	return s.provisionNew(ctx, subdomain, StatusActive)
}

// randomAlphaNum generates a random os-prefixed alphanumeric string of 6 characters.
func randomAlphaNum(n int) (_ string, err error) {
	defer derrors.Wrap(&err, "orgs.randomSubdomain()")

	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	suffix := make([]byte, n)
	for i, b := range buf {
		suffix[i] = chars[int(b)%len(chars)]
	}
	return "os" + string(suffix), nil
}

// Organization lifecycle statuses. These must stay in sync with the CHECK
// constraint added to that column in migrations.
const (
	// StatusReady is set on organizations provisioned by the Provisioner
	// that are sitting in its pool, but not yet assigned to a user.
	StatusReady = "ready"
	// StatusActive is set once an organization has been fully provisioned
	// and assigned to a user.
	StatusActive = "active"
	// StatusUpdatePending is set while an organization's headscale
	// config is being applied. If the restart fails, status is left
	// here rather than reverted, since the org's actual running
	// headscale config no longer necessarily matches what's stored.
	StatusUpdatePending = "update_pending"
)
