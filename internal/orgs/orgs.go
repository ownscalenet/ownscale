// Package orgs contains organization-provisioning logic shared by the CLI
// (ownscale create-organization) and the web server (auto-provisioning a
// new organization for a first-time login).
package orgs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/derrors"
	"github.com/ownscalenet/ownscale/internal/dockerctl"

	"github.com/google/uuid"
)

// Create provisions an organization: it inserts the organizations row, starts
// the headscale container for subdomain via ctl, and records the container
// id, all within a single transaction against pool. Queries run against the
// transaction are logged at debug level via logger.
func Create(ctx context.Context, pool *sql.DB, ctl dockerctl.Controller, baseDomain, subdomain string, logger *slog.Logger) (_ sqlcgen.Organization, err error) {
	defer derrors.Wrap(&err, "orgs.Create(ctx, pool, ctl, %q, %q, logger)", baseDomain, subdomain)

	if err := dockerctl.ValidateSubdomain(subdomain); err != nil {
		return sqlcgen.Organization{}, err
	}

	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return sqlcgen.Organization{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	qtx := sqlcgen.New(db.NewLoggingDBTX(tx, logger))

	org, err := qtx.CreateOrganization(ctx, sqlcgen.CreateOrganizationParams{
		ID:        uuid.Must(uuid.NewV7()).String(),
		Subdomain: subdomain,
	})
	if err != nil {
		return sqlcgen.Organization{}, fmt.Errorf("create organization record (already exists?): %w", err)
	}

	containerID, err := ctl.CreateOrganization(ctx, qtx, org.ID, subdomain, baseDomain)
	if err != nil {
		return sqlcgen.Organization{}, fmt.Errorf("create container: %w", err)
	}
	if err := qtx.SetOrganizationContainerID(ctx, sqlcgen.SetOrganizationContainerIDParams{
		ContainerID: containerID,
		ID:          org.ID,
	}); err != nil {
		return sqlcgen.Organization{}, fmt.Errorf("store container id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return sqlcgen.Organization{}, fmt.Errorf("commit transaction: %w", err)
	}
	org.ContainerID = containerID
	return org, nil
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
