// Package orgs contains organization-provisioning logic shared by the CLI
// (osa organization create) and the web server (auto-provisioning a new
// organization for a first-time login).
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

// CreatePending inserts an organizations row (status "pending") for
// subdomain and returns it. It deliberately does none of the Docker/network
// work that used to happen inline in Create: creating an organization's
// isolated Docker network is picked up by the host OS as a network change,
// which causes browsers (notably Firefox) to drop in-flight requests --
// including the very request creating the org. Keeping this fast and
// DB-only lets callers (the web handler) return a response before that
// network change happens, and hand the actual provisioning off to
// Provision, run either synchronously (CLI) or from the background
// Provisioner (web).
func (s *Service) CreatePending(ctx context.Context, subdomain string) (_ sqlcgen.Organization, err error) {
	defer derrors.Wrap(&err, "s.CreatePending(%q)", subdomain)

	if err := dockerctl.ValidateSubdomain(subdomain); err != nil {
		return sqlcgen.Organization{}, err
	}

	org, err := s.queries.CreateOrganization(ctx, sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: subdomain,
	})
	if err != nil {
		return org, fmt.Errorf("create organization record (already exists?): %w", err)
	}
	return org, nil
}

// Provision does the actual work of standing up organization orgID: it
// creates its OIDC client and its headscale container (via ctl),
// then marks the organization "active". If any step fails, it marks the
// organization "failed" (best-effort; the original error is still
// returned) instead of leaving it stuck "pending" forever.
func (s *Service) Provision(ctx context.Context, orgID string) (err error) {
	defer derrors.Wrap(&err, "s.Provision(%q)", orgID)

	org, err := s.queries.GetOrganization(ctx, orgID)
	if err != nil {
		return fmt.Errorf("get organization: %w", err)
	}
	// Guards against duplicate Provision calls for the same organization
	// (e.g. a duplicate Signal, or a signal racing a poll tick): once an
	// organization is active (or has permanently failed), there's nothing
	// left to do.
	if org.Status != StatusPending {
		return nil
	}

	if err := s.provision(ctx, org); err != nil {
		// Clean up whatever provision managed to create.
		if cleanupErr := s.ctl.RemoveOrganization(ctx, org.Subdomain); cleanupErr != nil {
			err = fmt.Errorf("%w (also failed to clean up partially-provisioned resources: %w)", err, cleanupErr)
		}
		if _, statusErr := s.queries.UpdateOrganizationStatus(ctx, sqlcgen.UpdateOrganizationStatusParams{
			ID:     orgID,
			Status: StatusFailed,
		}); statusErr != nil {
			return fmt.Errorf("%w (also failed to record failed status: %w)", err, statusErr)
		}
		return err
	}

	if _, err := s.queries.UpdateOrganizationStatus(ctx, sqlcgen.UpdateOrganizationStatusParams{
		ID:     orgID,
		Status: StatusActive,
	}); err != nil {
		return fmt.Errorf("mark organization active: %w", err)
	}
	return nil
}

// provision creates org's OIDC client (within its own transaction) and its
// headscale container via ctl. It's split out of Provision so
// the latter can uniformly handle the success/failure status update
// regardless of which step fails.
func (s *Service) provision(ctx context.Context, org sqlcgen.Organization) error {
	subdomain := org.Subdomain

	clientID, err := randomAlphaNum(8)
	if err != nil {
		return err
	}
	clientID = "os_" + subdomain + "_" + clientID
	clientSecret, err := randomAlphaNum(32)
	if err != nil {
		return err
	}
	clientSecretHash, err := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	redirectURIsJSON, err := json.Marshal([]string{
		"https://" + subdomain + "." + s.baseDomain + "/oidc/callback",
		"https://" + subdomain + "." + s.baseDomain + "/admin/oidc/callback",
	})
	if err != nil {
		return err
	}
	if _, err := s.queries.CreateOIDCClient(ctx, sqlcgen.CreateOIDCClientParams{
		ID:               xid.New().String(),
		OrganizationID:   org.ID,
		ClientID:         clientID,
		ClientSecretHash: string(clientSecretHash),
		RedirectUris:     string(redirectURIsJSON),
	}); err != nil {
		return err
	}

	oidcCreds := dockerctl.OIDCClientCredentials{
		Issuer:       s.issuer,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}

	// Seed the config.yaml orgconfigs row with the same content
	// CreateOrganization independently renders (via the same pure,
	// deterministic BuildHeadscaleConfig call) and writes to disk, so the
	// database has an authoritative copy from the moment the organization
	// exists -- GetDNSSettings/SetDNSSettings/SetAuthRestrictions never
	// read the file back, only this row.
	hsConfig, err := dockerctl.BuildHeadscaleConfig(subdomain, subdomain+"."+s.baseDomain, oidcCreds, s.derpCfg)
	if err != nil {
		return err
	}
	if _, err := s.queries.CreateOrgConfig(ctx, sqlcgen.CreateOrgConfigParams{
		ID:             xid.New().String(),
		OrganizationID: org.ID,
		Path:           dockerctl.ConfigYAMLPath,
		Content:        hsConfig,
	}); err != nil {
		return err
	}

	if _, err := s.ctl.CreateOrganization(ctx, subdomain, s.baseDomain, oidcCreds, s.derpCfg); err != nil {
		return err
	}
	return nil
}

// Create provisions an organization end-to-end: it inserts the
// organizations row (CreatePending) and then synchronously runs the rest
// of provisioning (Provision) before returning. It's used by callers that
// want to block until provisioning is fully done or failed (e.g. the CLI),
// as opposed to the web handler, which calls CreatePending and hands
// Provision off to the background Provisioner instead.
func (s *Service) Create(ctx context.Context, subdomain string) (_ sqlcgen.Organization, err error) {
	defer derrors.Wrap(&err, "s.Create(%q)", subdomain)

	org, err := s.CreatePending(ctx, subdomain)
	if err != nil {
		return org, err
	}
	if err := s.Provision(ctx, org.ID); err != nil {
		return org, err
	}
	org.Status = StatusActive
	return org, nil
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
	// StatusPending is the initial status of an organization that has
	// only been recorded in the database (see Service.CreatePending) but
	// not yet provisioned.
	StatusPending = "pending"
	// StatusActive is set once an organization has been fully provisioned.
	StatusActive = "active"
	// StatusFailed is set if provisioning an organization fails permanently.
	StatusFailed = "failed"
)
