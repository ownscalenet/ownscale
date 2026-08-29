package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/peterbourgon/ff/v4"

	"github.com/ownscalenet/ownscale/db"
)

const (
	// Has to match what headscale creates.
	headscaleDBFilename = "db.sqlite"
)

// restoreRunner restores a single database at localPath from replicaURL.
// It's a seam so tests can fake process execution instead of shelling out
// to the real litestream binary.
type restoreRunner func(ctx context.Context, localPath, replicaURL string) error

func litestreamRestoreRunner(ctx context.Context, localPath, replicaURL string) error {
	cmd := exec.CommandContext(ctx, "litestream", "restore", "-if-replica-exists", "-o", localPath, replicaURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("litestream restore %s: %w", replicaURL, err)
	}
	return nil
}

// restoreIfMissing restores localPath (dataDir/relPath) from
// "<replicaBaseURL>/<relPath>" via run, but only when localPath doesn't
// already exist and replicaBaseURL is non-empty (no replica configured is
// the common case for local dev without a backup destination, and is not
// an error). This is backend-agnostic: it never lists or otherwise
// inspects the replica store, so it works the same regardless of
// replicaBaseURL's scheme (file://, s3://, abs://, etc) -- litestream
// itself handles that.
func restoreIfMissing(ctx context.Context, out io.Writer, run restoreRunner, replicaBaseURL, dataDir, relPath string) error {
	if replicaBaseURL == "" {
		fmt.Fprintf(out, "no replica configured (LITESTREAM_REPLICA_URL unset), skipping restore of %s\n", relPath)
		return nil
	}

	localPath := filepath.Join(dataDir, relPath)
	if _, err := os.Stat(localPath); err == nil {
		return nil // already present locally, nothing to restore
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", localPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", localPath, err)
	}

	replicaURL := replicaBaseURL + "/" + relPath
	fmt.Fprintf(out, "restoring %s from %s\n", localPath, replicaURL)
	return run(ctx, localPath, replicaURL)
}

// restoreOrganizations restores every organization's headscale database
// that's missing locally, using queries (already pointed at a migrated database,
// so the organizations table is known to  exist and be current) to discover which subdomains exist.
func restoreOrganizations(ctx context.Context, out io.Writer, run restoreRunner, replicaBaseURL, dataDir string, queries *db.TxQueries) error {
	organizations, err := queries.ListOrganizations(ctx)
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}

	var errs []error
	for _, org := range organizations {
		if err := restoreIfMissing(ctx, out, run, replicaBaseURL, dataDir,
			filepath.Join("headscale", org.Subdomain, headscaleDBFilename)); err != nil {
			errs = append(errs, fmt.Errorf("organization %s: %w", org.Subdomain, err))
		}
	}
	return errors.Join(errs...)
}

// newRestoreCommand builds the "restore" subcommand group (db/organizations),
// used by entrypoint.sh on every container start to recover from litestream
// backup any database that's missing locally (e.g. after data loss),
// without disturbing databases that are already present.
func newRestoreCommand(rootFlags *ff.FlagSet, configPath *string) *ff.Command {
	restoreFlags := ff.NewFlagSet("restore").SetParent(rootFlags)
	restoreCmd := &ff.Command{
		Name:      "restore",
		Usage:     "osa restore SUBCOMMAND ...",
		ShortHelp: "recover databases from litestream backup if missing locally",
		Flags:     restoreFlags,
	}
	restoreCmd.Subcommands = append(restoreCmd.Subcommands,
		newRestoreDBCommand(restoreFlags, configPath),
		newRestoreOrganizationsCommand(restoreFlags, configPath),
	)
	return restoreCmd
}

func newRestoreDBCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
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
			cfg, _, err := loadConfigAndLogger(configPath)
			if err != nil {
				return err
			}
			return restoreIfMissing(ctx, os.Stdout, litestreamRestoreRunner,
				os.Getenv("LITESTREAM_REPLICA_URL"), cfg.DataDir, "ownscale.sqlite")
		},
	}
}

func newRestoreOrganizationsCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
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
			res, cleanup, err := newOrgResources(configPath, true, false)
			defer cleanup()
			if err != nil {
				return err
			}
			return restoreOrganizations(ctx, os.Stdout, litestreamRestoreRunner,
				os.Getenv("LITESTREAM_REPLICA_URL"), res.cfg.DataDir, res.queries)
		},
	}
}
