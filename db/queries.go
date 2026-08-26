package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// TxQueries is like *sqlcgen.Queries, but it has transaction and logging of executed queries.
type TxQueries struct {
	*sqlcgen.Queries
	pool   *sql.DB
	logger *slog.Logger
}

// New builds a TxQueries backed by pool, logging every statement at debug
// level via logger.
func New(pool *sql.DB, logger *slog.Logger) *TxQueries {
	return &TxQueries{
		Queries: sqlcgen.New(newLoggingDBTX(pool, logger)),
		pool:    pool,
		logger:  logger,
	}
}

func (q *TxQueries) StartTx(ctx context.Context) (_ *sqlcgen.Queries, commit, rollback func() error, err error) {
	tx, err := q.pool.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("begin transaction: %w", err)
	}
	return sqlcgen.New(newLoggingDBTX(tx, q.logger)), tx.Commit, tx.Rollback, nil
}
