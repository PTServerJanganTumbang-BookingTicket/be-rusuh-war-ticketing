package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBConn is the subset of *pgxpool.Pool that this package uses.
//
// Declaring the interface here (not in an outbound port) keeps it private to
// the adapter layer — it is an implementation detail, not a domain contract.
// *pgxpool.Pool satisfies it without any wrapper; tests supply their own
// implementation to drive every path without a live database.
type DBConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}
