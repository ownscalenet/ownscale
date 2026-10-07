package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/benbjohnson/litestream"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/derrors"
)

// Restore restores the database at relPath under dataDir from its replica,
// but only when it's missing locally. A database without a backup is left
// alone. Progress is written to out.
func Restore(ctx context.Context, cfg config.BackupConfig, out io.Writer, dataDir, relPath string) (err error) {
	defer derrors.Wrap(&err, "backup.Restore(%q)", relPath)

	if !cfg.Enabled() {
		fmt.Fprintf(out, "backups disabled, skipping restore of %s\n", relPath)
		return nil
	}
	path := filepath.Join(dataDir, relPath)
	if exists, err := fileExists(path); err != nil || exists {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	client, err := newClient(cfg, relPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "restoring %s\n", relPath)
	opt := litestream.NewRestoreOptions()
	opt.OutputPath = path
	err = litestream.NewReplicaWithClient(nil, client).Restore(ctx, opt)
	if errors.Is(err, litestream.ErrTxNotAvailable) || errors.Is(err, litestream.ErrNoSnapshots) {
		fmt.Fprintf(out, "%s has no backup, skipping\n", relPath)
		return nil
	}
	return err
}

// Verify fails when the database at relPath under dataDir is missing
// locally although its replica holds a backup, which means data was lost
// and `osa restore` should be run. A missing database without a backup is
// new, so it passes.
func Verify(ctx context.Context, cfg config.BackupConfig, out io.Writer, dataDir, relPath string) (err error) {
	defer derrors.Wrap(&err, "backup.Verify(%q)", relPath)

	if !cfg.Enabled() {
		return nil
	}
	path := filepath.Join(dataDir, relPath)
	exists, err := fileExists(path)
	if err != nil {
		return err
	}
	client, err := newClient(cfg, relPath)
	if err != nil {
		return err
	}
	backedUp, err := hasBackup(ctx, client)
	if err != nil {
		return err
	}

	switch {
	case !exists && backedUp:
		return fmt.Errorf("%s is missing but has a backup, run `osa restore` to recover it", relPath)
	case !exists:
		fmt.Fprintf(out, "%s has no backup yet, treating it as a new database\n", relPath)
	case backedUp:
		// Restored databases have no local Litestream state until
		// replication starts, so this is only worth a mention.
		_, maxTXID, err := litestream.NewDB(path).MaxLTX()
		if err != nil {
			return err
		}
		if maxTXID == 0 {
			fmt.Fprintf(out, "%s has a backup but was never replicated from this machine\n", relPath)
		}
	}
	return nil
}

// hasBackup reports whether client's replica holds any LTX file. Older
// files are compacted into higher levels, so every level is checked.
func hasBackup(ctx context.Context, client litestream.ReplicaClient) (bool, error) {
	for level := 0; level <= litestream.SnapshotLevel; level++ {
		itr, err := client.LTXFiles(ctx, level, 0, false)
		if err != nil {
			return false, err
		}
		found := itr.Next()
		if err := errors.Join(itr.Err(), itr.Close()); err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
