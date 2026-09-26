package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/peterbourgon/ff/v4"

	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/derrors"
)

type statusChecker func(ctx context.Context, localPath, replicaURL string) (status string, err error)
type replicaHasBackup func(ctx context.Context, replicaURL string) (bool, error)

func litestreamStatus(ctx context.Context, localPath, replicaURL string) (string, error) {
	ephemeralConfig := fmt.Sprintf("dbs:\n  - path: %s\n    replica:\n      url: %s\n", localPath, replicaURL)

	cmd := exec.CommandContext(ctx, "litestream", "status", "-config", "/dev/stdin", "-json")
	cmd.Stdin = strings.NewReader(ephemeralConfig)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("litestream status %s: %w: %s", localPath, err, stderr.String())
	}

	var results []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		return "", fmt.Errorf("parse litestream status output for %s: %w", localPath, err)
	}
	if len(results) != 1 {
		return "", fmt.Errorf("litestream status %s: expected exactly 1 result, got %d", localPath, len(results))
	}
	return results[0].Status, nil
}

// wasBackedUp lists the LTX files present at replicaURL directly
// (this works standalone, without any config file or local database, unlike "status").
// An empty list means nothing has ever been backed up to this path.
func wasBackedUp(ctx context.Context, replicaURL string) (_ bool, err error) {
	defer derrors.Wrap(&err, "wasBackedUp(%s)", replicaURL)
	cmd := exec.CommandContext(ctx, "litestream", "ltx", "-json", replicaURL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("litestream ltx %s: %w: %s", replicaURL, err, stderr.String())
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		return false, fmt.Errorf("parse litestream ltx output for %s: %w", replicaURL, err)
	}
	return len(entries) > 0, nil
}

// verifyHealthy checks that the database at dataDir/relPath is healthy
// according to litestream, but only fails when it's actually a problem:
//   - replicaBaseURL == "": no replica configured at all, skip (nothing to
//     verify).
//   - status "ok": healthy, pass.
//   - status != "ok" but the replica has no backup content for this path
//     yet: a brand-new database (fresh install or new organization) that
//     simply hasn't been created/backed up yet, not an error.
//   - status != "ok" and the replica already has backup content: real
//     data loss/corruption -- fail, instructing the operator to run the
//     matching `osa restore` command manually.
func verifyHealthy(ctx context.Context,
	out io.Writer,
	checkStatus statusChecker,
	hasBackup replicaHasBackup,
	replicaBaseURL, dataDir, relPath string) (err error) {
	defer derrors.Wrap(&err, "verifyHealthy(%s, %s, %s)", replicaBaseURL, dataDir, relPath)
	if replicaBaseURL == "" {
		return nil
	}

	localPath := filepath.Join(dataDir, relPath)
	replicaURL, err := url.JoinPath(replicaBaseURL, relPath)
	if err != nil {
		return err
	}

	status, err := checkStatus(ctx, localPath, replicaURL)
	if err != nil {
		return fmt.Errorf("check %s: %w", relPath, err)
	}
	if status == "ok" {
		return nil
	}

	backed, err := hasBackup(ctx, replicaURL)
	if err != nil {
		return fmt.Errorf("check %s: %w", relPath, err)
	}
	if !backed {
		fmt.Fprintf(out, "%s: no backup exists yet (status=%q), treating as a new database\n", relPath, status)
		return nil
	}

	return fmt.Errorf("%s: unhealthy (status=%q) but a backup exists at %s -- run `osa restore` to recover", relPath, status, replicaURL)
}

// verifyOrganizationsHealthy verifies every organization's headscale database,
// using queries (already pointed at a migrated database) to discover which subdomains exist.
func verifyOrganizationsHealthy(ctx context.Context,
	out io.Writer,
	checkStatus statusChecker,
	hasBackup replicaHasBackup,
	replicaBaseURL, dataDir string, queries *db.TxQueries) error {
	organizations, err := queries.ListOrganizations(ctx)
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}

	var errs []error
	for _, org := range organizations {
		if err := verifyHealthy(ctx, out, checkStatus, hasBackup, replicaBaseURL, dataDir,
			filepath.Join("headscale", org.Subdomain, headscaleDBFilename)); err != nil {
			errs = append(errs, fmt.Errorf("organization %s: %w", org.Subdomain, err))
		}
	}
	return errors.Join(errs...)
}

// newVerifyCommand builds the "verify" subcommand group (db/organizations),
// used by entrypoint.sh on every container start to fail fast (before the
// app runs) if a database looks unhealthy despite an existing backup.
// Unlike "restore", this never modifies anything -- recovering from a
// failure here is a manual `osa restore` invocation.
func newVerifyCommand(rootFlags *ff.FlagSet) *ff.Command {
	verifyFlags := ff.NewFlagSet("verify").SetParent(rootFlags)
	verifyCmd := &ff.Command{
		Name:      "verify",
		Usage:     "osa verify SUBCOMMAND ...",
		ShortHelp: "fail if a database looks unhealthy despite having a litestream backup",
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
		ShortHelp: "verify ownscale's own database is healthy",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa verify db")
			}
			cfg, _, err := loadConfigAndLogger()
			if err != nil {
				return err
			}
			return verifyHealthy(ctx, os.Stdout, litestreamStatus, wasBackedUp,
				os.Getenv("LITESTREAM_REPLICA_URL"), cfg.DataDir, "ownscale.sqlite")
		},
	}
}

func newVerifyOrganizationsCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("verify organizations").SetParent(parent)
	return &ff.Command{
		Name:      "organizations",
		Usage:     "osa verify organizations",
		ShortHelp: "verify every organization's headscale database is healthy",
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
			return verifyOrganizationsHealthy(ctx, os.Stdout, litestreamStatus, wasBackedUp,
				os.Getenv("LITESTREAM_REPLICA_URL"), res.cfg.DataDir, res.queries)
		},
	}
}
