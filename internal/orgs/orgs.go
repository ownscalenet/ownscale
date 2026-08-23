// Package orgs contains organization-provisioning logic shared by the CLI
// (osa organization create) and the web server (auto-provisioning a new
// organization for a first-time login).
package orgs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/derrors"
	"github.com/ownscalenet/ownscale/internal/dockerctl"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Service provisions organizations against a fixed set of queries, Docker
// controller, and base domain. It's shared by the CLI (cmd/osa) and the web
// server (internal/handlers) so both wire it up once instead of threading
// the same dependencies through every call site.
type Service struct {
	queries    *db.TxQueries
	ctl        *dockerctl.Ctl
	baseDomain string
	issuer     string
}

// NewService builds a Service backed by queries and ctl, provisioning
// organizations under baseDomain.
func NewService(queries *db.TxQueries, ctl *dockerctl.Ctl, baseDomain, issuer string) *Service {
	return &Service{queries: queries, ctl: ctl, baseDomain: baseDomain, issuer: issuer}
}

// BaseDomain returns the domain organizations are provisioned under (e.g.
// "example.com" for "<sub>.example.com").
func (s *Service) BaseDomain() string {
	return s.baseDomain
}

// Create provisions an organization: it inserts the organizations row and
// starts the headscale container for subdomain via ctl, all within a
// single transaction.
func (s *Service) Create(ctx context.Context, subdomain string) (_ sqlcgen.Organization, err error) {
	defer derrors.Wrap(&err, "(*orgs.Service).Create(ctx, %q)", subdomain)

	if err := dockerctl.ValidateSubdomain(subdomain); err != nil {
		return sqlcgen.Organization{}, err
	}

	var org sqlcgen.Organization
	qtx, commit, rollback, err := s.queries.StartTx(ctx)
	if err != nil {
		return org, err
	}
	defer rollback() // safe, rollback becomes no-op if already comitted.

	org, err = qtx.CreateOrganization(ctx, sqlcgen.CreateOrganizationParams{
		ID:        uuid.Must(uuid.NewV7()).String(),
		Subdomain: subdomain,
	})
	if err != nil {
		return org, fmt.Errorf("create organization record (already exists?): %w", err)
	}

	clientID, err := randomHex(8)
	if err != nil {
		return org, err
	}
	clientID = "os_" + subdomain + "_" + clientID
	clientSecret, err := randomHex(32)
	if err != nil {
		return org, err
	}
	clientSecretHash, err := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	if err != nil {
		return org, err
	}
	redirectURIsJSON, err := json.Marshal([]string{
		"https://" + subdomain + "." + s.baseDomain + "/oidc/callback",
		"https://" + subdomain + "." + s.baseDomain + "/admin/oidc/callback",
	})
	if err != nil {
		return org, err
	}
	if _, err := qtx.CreateOIDCClient(ctx, sqlcgen.CreateOIDCClientParams{
		ID:               uuid.Must(uuid.NewV7()).String(),
		OrganizationID:   org.ID,
		ClientID:         clientID,
		ClientSecretHash: string(clientSecretHash),
		RedirectUris:     string(redirectURIsJSON),
	}); err != nil {
		return org, err
	}

	if _, err := s.ctl.CreateOrganization(ctx, subdomain, s.baseDomain, dockerctl.OIDCClientCredentials{
		Issuer:       s.issuer,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}); err != nil {
		return org, fmt.Errorf("create container: %w", err)
	}

	return org, commit()
}

func randomHex(n int) (_ string, err error) {
	defer derrors.Wrap(&err, "orgs.randomHex(%d)", n)

	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
