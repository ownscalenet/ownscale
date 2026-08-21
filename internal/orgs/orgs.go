// Package orgs contains organization-provisioning logic shared by the CLI
// (osa organization create) and the web server (auto-provisioning a new
// organization for a first-time login).
package orgs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/derrors"
	"github.com/ownscalenet/ownscale/internal/dockerctl"

	"github.com/google/uuid"
)

// Service provisions organizations against a fixed set of queries, Docker
// controller, and base domain. It's shared by the CLI (cmd/osa) and the web
// server (internal/handlers) so both wire it up once instead of threading
// the same dependencies through every call site.
type Service struct {
	queries    *db.TxQueries
	ctl        *dockerctl.Ctl
	baseDomain string
}

// NewService builds a Service backed by queries and ctl, provisioning
// organizations under baseDomain.
func NewService(queries *db.TxQueries, ctl *dockerctl.Ctl, baseDomain string) *Service {
	return &Service{queries: queries, ctl: ctl, baseDomain: baseDomain}
}

// BaseDomain returns the domain organizations are provisioned under (e.g.
// "example.com" for "<sub>.example.com").
func (s *Service) BaseDomain() string {
	return s.baseDomain
}

// Create provisions an organization: it inserts the organizations row, starts
// the headscale container for subdomain via ctl, and records the container
// id, all within a single transaction.
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

	containerID, err := s.ctl.CreateOrganization(ctx, subdomain, s.baseDomain)
	if err != nil {
		return org, fmt.Errorf("create container: %w", err)
	}
	if err := qtx.SetOrganizationContainerID(ctx, sqlcgen.SetOrganizationContainerIDParams{
		ContainerID: containerID,
		ID:          org.ID,
	}); err != nil {
		return org, fmt.Errorf("store container id: %w", err)
	}

	return org, commit()
}

// RandomSubdomain generates a short, DNS-label-safe random subdomain (e.g.
// "org-3f9a21c8") suitable for auto-provisioning a new organization.
func RandomSubdomain() (_ string, err error) {
	defer derrors.Wrap(&err, "orgs.RandomSubdomain()")

	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "org-" + hex.EncodeToString(buf), nil
}
