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

// restoreOrganizations restores every organization's headscale database
// that's missing locally, using queries (already pointed at a migrated
// database) to discover which subdomains exist.
func restoreOrganizations(ctx context.Context, out io.Writer, cfg config.BackupConfig, dataDir string, queries *db.TxQueries) error {
	organizations, err := queries.ListOrganizations(ctx)
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}

	var errs []error
	for _, org := range organizations {
		if err := backup.Restore(ctx, cfg, out, dataDir, orgs.DBFile(org.Subdomain)); err != nil {
			errs = append(errs, fmt.Errorf("organization %s: %w", org.Subdomain, err))
		}
	}
	return errors.Join(errs...)
}

// newRestoreCommand builds the "restore" subcommand group (db/organizations),
// run manually, with ownscale stopped, to recover from backup any database
// that's missing locally (e.g. after data loss), without disturbing
// databases that are already present.
func newRestoreCommand(rootFlags *ff.FlagSet) *ff.Command {
	restoreFlags := ff.NewFlagSet("restore").SetParent(rootFlags)
	restoreCmd := &ff.Command{
		Name:      "restore",
		Usage:     "osa restore SUBCOMMAND ...",
		ShortHelp: "recover databases from backup if missing locally",
		Flags:     restoreFlags,
	}
	restoreCmd.Subcommands = append(restoreCmd.Subcommands,
		newRestoreDBCommand(restoreFlags),
		newRestoreOrganizationsCommand(restoreFlags),
	)
	return restoreCmd
}

func newRestoreDBCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("restore db").SetParent(parent)
	return &ff.Command{
		Name:      "db",
		Usage:     "osa restore db",
		ShortHelp: "restore ownscale's own database if missing locally",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa restore db")
			}
			cfg, _, err := loadConfigAndLogger()
			if err != nil {
				return err
			}
			return backup.Restore(ctx, cfg.Backup, os.Stdout, cfg.DataDir, config.DatabaseFile)
		},
	}
}

func newRestoreOrganizationsCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("restore organizations").SetParent(parent)
	return &ff.Command{
		Name:      "organizations",
		Usage:     "osa restore organizations",
		ShortHelp: "restore every organization's headscale database if missing locally",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa restore organizations")
			}
			res, cleanup, err := newOrgResources()
			defer cleanup()
			if err != nil {
				return err
			}
			return restoreOrganizations(ctx, os.Stdout, res.cfg.Backup, res.cfg.DataDir, res.queries)
		},
	}
}
