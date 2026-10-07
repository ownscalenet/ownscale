package db

import (
	"database/sql"
	"embed"
	"os"
	"path/filepath"

	// Register the "sqlite" database/sql driver used by sql.Open in Connect.
	// DECISIONS#7
	_ "modernc.org/sqlite"

	"github.com/ownscalenet/ownscale/derrors"
)

// MigrationsFS embeds the SQL migration files (db/migrations),
// rooted one level above the migrations themselves.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// Connect opens the SQLite database at path (creating the parent directory
// and the file itself if needed), enabling WAL mode and foreign keys.
func Connect(path string) (_ *sql.DB, err error) {
	defer derrors.Wrap(&err, "db.Connect(%q)", path)

	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	pool, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite only allows one writer at a time; serialize access through a
	// single connection to avoid "database is locked" errors under
	// concurrent writes.
	pool.SetMaxOpenConns(1)
	if err := pool.Ping(); err != nil {
		return nil, err
	}
	return pool, nil
}
