package db

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// loggingDBTX wraps a sqlcgen.DBTX (either a *sql.DB or a *sql.Tx), logging
// every statement it executes at debug level (statement text, argument
// count, and duration) before delegating to the wrapped connection.
type loggingDBTX struct {
	db     sqlcgen.DBTX
	logger *slog.Logger
}

// newLoggingDBTX wraps dbtx so every query it runs is logged at debug level
// via logger. Pass the result to sqlcgen.New (instead of the raw *sql.DB or
// *sql.Tx) to enable query logging for that Queries instance.
func newLoggingDBTX(dbtx sqlcgen.DBTX, logger *slog.Logger) sqlcgen.DBTX {
	return &loggingDBTX{db: dbtx, logger: logger}
}

func (l *loggingDBTX) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := l.db.ExecContext(ctx, query, args...)
	l.log(ctx, "exec", query, len(args), time.Since(start), err)
	return res, err
}

func (l *loggingDBTX) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	start := time.Now()
	stmt, err := l.db.PrepareContext(ctx, query)
	l.log(ctx, "prepare", query, 0, time.Since(start), err)
	return stmt, err
}

func (l *loggingDBTX) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := l.db.QueryContext(ctx, query, args...)
	l.log(ctx, "query", query, len(args), time.Since(start), err)
	return rows, err
}

// QueryRowContext has no error to report directly: *sql.Row defers error
// reporting to the eventual Scan call, so it's logged without one.
func (l *loggingDBTX) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := l.db.QueryRowContext(ctx, query, args...)
	l.log(ctx, "query_row", query, len(args), time.Since(start), nil)
	return row
}

func (l *loggingDBTX) log(ctx context.Context, op, query string, argCount int, dur time.Duration, err error) {
	attrs := []any{"op", op, "query", query, "args", argCount, "duration", dur}
	if err != nil {
		attrs = append(attrs, "error", err)
	}
	l.logger.DebugContext(ctx, "db query", attrs...)
}
