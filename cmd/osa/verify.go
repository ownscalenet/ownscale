package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/peterbourgon/ff/v4"

	"github.com/ownscalenet/ownscale/backup"
	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/orgs"
)

// verifyOrganizations verifies every organization's headscale database,
// using queries (already pointed at a migrated database) to discover which
// subdomains exist.
func verifyOrganizations(ctx context.Context, out io.Writer, cfg config.BackupConfig, dataDir string, queries *db.TxQueries) error {
	organizations, err := queries.ListOrganizations(ctx)
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}

	var errs []error
	for _, org := range organizations {
		if err := backup.Verify(ctx, cfg, out, dataDir, orgs.DBFile(org.Subdomain)); err != nil {
			errs = append(errs, fmt.Errorf("organization %s: %w", org.Subdomain, err))
		}
	}
	return errors.Join(errs...)
}

// newVerifyCommand builds the "verify" subcommand group (db/organizations),
// used by entrypoint.sh on every container start to fail fast (before the
// app runs) if a database is missing although it has a backup.
func newVerifyCommand(rootFlags *ff.FlagSet) *ff.Command {
	verifyFlags := ff.NewFlagSet("verify").SetParent(rootFlags)
	verifyCmd := &ff.Command{
		Name:      "verify",
		Usage:     "osa verify SUBCOMMAND ...",
		ShortHelp: "fail if a database is missing although it has a backup",
		Flags:     verifyFlags,
	}
	verifyCmd.Subcommands = append(verifyCmd.Subcommands,
		newVerifyDBCommand(verifyFlags),
		newVerifyOrganizationsCommand(verifyFlags),
	)
	return verifyCmd
}

func newVerifyDBCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("verify db").SetParent(parent)
	return &ff.Command{
		Name:      "db",
		Usage:     "osa verify db",
		ShortHelp: "verify ownscale's own database against its backup",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa verify db")
			}
			cfg, _, err := loadConfigAndLogger()
			if err != nil {
				return err
			}
			return backup.Verify(ctx, cfg.Backup, os.Stdout, cfg.DataDir, config.DatabaseFile)
		},
	}
}

func newVerifyOrganizationsCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("verify organizations").SetParent(parent)
	return &ff.Command{
		Name:      "organizations",
		Usage:     "osa verify organizations",
		ShortHelp: "verify every organization's headscale database against its backup",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa verify organizations")
			}
			res, cleanup, err := newOrgResources()
			defer cleanup()
			if err != nil {
				return err
			}
			return verifyOrganizations(ctx, os.Stdout, res.cfg.Backup, res.cfg.DataDir, res.queries)
		},
	}
}
