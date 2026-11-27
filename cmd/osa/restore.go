package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/peterbourgon/ff/v4"
	"golang.org/x/sync/errgroup"

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

	// Each restore spends most of its time waiting on the replica, so a few at
	// once cut boot time on a new instance roughly by restoreConcurrency.
	out = &lockedWriter{w: out}
	errs := make([]error, len(organizations))
	var g errgroup.Group
	g.SetLimit(restoreConcurrency)
	for i, org := range organizations {
		g.Go(func() error {
			if err := backup.Restore(ctx, cfg, out, dataDir, orgs.DBFile(org.Subdomain)); err != nil {
				errs[i] = fmt.Errorf("organization %s: %w", org.Subdomain, err)
			}
			return nil // keep restoring the others, like a serial loop would
		})
	}
	g.Wait()
	return errors.Join(errs...)
}

// restoreConcurrency is how many organization databases are restored at once.
const restoreConcurrency = 4

// lockedWriter keeps concurrent restores' progress lines from interleaving.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
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
		ShortHelp: "restore Ownscale's own database if missing locally",
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
