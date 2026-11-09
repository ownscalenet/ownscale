// Package backup continuously replicates the SQLite databases under the
// data directory with Litestream, and restores or verifies them against
// their replica.
//
// Litestream opens its own connections to every database it replicates.
// SQLite's POSIX locks are per process, so the app must use the same SQLite
// implementation (modernc.org/sqlite) or closing one connection drops the
// other's locks.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"

	"github.com/benbjohnson/litestream"
	"github.com/benbjohnson/litestream/abs"
	"github.com/benbjohnson/litestream/file"
	"github.com/benbjohnson/litestream/s3"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/derrors"
)

// Replicator replicates databases under a data directory to the configured
// replica. With backups disabled, or on a nil Replicator, all its methods
// are no-ops.
type Replicator struct {
	cfg     config.BackupConfig
	dataDir string
	store   *litestream.Store
	// done is closed to abort the final sync on Close.
	done chan struct{}
}

// New starts a Replicator for databases under dataDir. Databases are
// replicated once passed to Add.
func New(ctx context.Context, cfg config.BackupConfig, dataDir string, logger *slog.Logger) (_ *Replicator, err error) {
	defer derrors.Wrap(&err, "backup.New(%q)", dataDir)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	r := &Replicator{cfg: cfg, dataDir: dataDir}
	if !cfg.Enabled() {
		logger.Warn("backups are disabled, set OWNSCALE_BACKUP_REPLICA_URL to enable them")
		return r, nil
	}

	r.store = litestream.NewStore(nil, litestream.DefaultCompactionLevels)
	// Litestream logs every sync at info level.
	r.store.Logger = slog.New(minLevelHandler{Handler: logger.Handler(), min: slog.LevelWarn}).With("component", "backup")
	r.done = make(chan struct{})
	r.store.SetDone(r.done)
	if err := r.store.Open(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// Add starts replicating the database at relPath, relative to the data
// directory. Adding a database again is a no-op.
func (r *Replicator) Add(relPath string) (err error) {
	defer derrors.Wrap(&err, "r.Add(%q)", relPath)

	if r == nil || r.store == nil {
		return nil
	}
	path := filepath.Join(r.dataDir, relPath)
	if r.store.FindDB(path) != nil {
		return nil
	}
	client, err := newClient(r.cfg, relPath)
	if err != nil {
		return err
	}
	db := litestream.NewDB(path)
	replica := litestream.NewReplicaWithClient(db, client)
	replica.SyncInterval = r.cfg.SyncInterval
	db.Replica = replica
	if fc, ok := client.(*file.ReplicaClient); ok {
		fc.Replica = replica // keeps the database's file ownership on the replica
	}
	return r.store.RegisterDB(db)
}

// Remove stops replicating the database at relPath and deletes its replica.
func (r *Replicator) Remove(ctx context.Context, relPath string) (err error) {
	defer derrors.Wrap(&err, "r.Remove(%q)", relPath)

	if r == nil || r.store == nil {
		return nil
	}
	if err := r.store.UnregisterDB(ctx, filepath.Join(r.dataDir, relPath)); err != nil {
		return err
	}
	return DeleteReplica(ctx, r.cfg, relPath)
}

// DeleteReplica deletes the replica of the database at relPath. It's a
// no-op with backups disabled.
func DeleteReplica(ctx context.Context, cfg config.BackupConfig, relPath string) (err error) {
	defer derrors.Wrap(&err, "DeleteReplica(%q)", relPath)

	if !cfg.Enabled() {
		return nil
	}
	client, err := newClient(cfg, relPath)
	if err != nil {
		return err
	}
	return client.DeleteAll(ctx)
}

// Close syncs pending changes to the replica and stops replicating. The
// final sync is abandoned once ctx is done. Callers must have closed their
// own connections to the replicated databases first.
func (r *Replicator) Close(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "r.Close()")

	if r == nil || r.store == nil {
		return nil
	}
	stop := context.AfterFunc(ctx, func() { close(r.done) })
	defer stop()
	return r.store.Close(ctx)
}

// newClient returns a client for relPath's replica, below cfg.ReplicaURL.
func newClient(cfg config.BackupConfig, relPath string) (litestream.ReplicaClient, error) {
	u, err := url.Parse(cfg.ReplicaURL)
	if err != nil {
		return nil, fmt.Errorf("parse replica URL: %w", err)
	}
	u = u.JoinPath(filepath.ToSlash(relPath))
	// Passed as query parameters so Litestream applies its defaults for
	// S3-compatible providers.
	if u.Scheme == "s3" {
		q := u.Query()
		if cfg.Region != "" {
			q.Set("region", cfg.Region)
		}
		if cfg.Endpoint != "" {
			q.Set("endpoint", cfg.Endpoint)
		}
		u.RawQuery = q.Encode()
	}

	client, err := litestream.NewReplicaClientFromURL(u.String())
	if err != nil {
		return nil, err
	}
	switch c := client.(type) {
	case *s3.ReplicaClient:
		if cfg.AccessKeyID != "" {
			c.AccessKeyID = cfg.AccessKeyID
		}
		if cfg.SecretAccessKey != "" {
			c.SecretAccessKey = cfg.SecretAccessKey
		}
	case *abs.ReplicaClient:
		c.AccountKey = cfg.AzureAccountKey
		c.Endpoint = cfg.Endpoint
	}
	return client, nil
}

// minLevelHandler drops records below min.
type minLevelHandler struct {
	slog.Handler
	min slog.Level
}

func (h minLevelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h minLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return minLevelHandler{Handler: h.Handler.WithAttrs(attrs), min: h.min}
}

func (h minLevelHandler) WithGroup(name string) slog.Handler {
	return minLevelHandler{Handler: h.Handler.WithGroup(name), min: h.min}
}
