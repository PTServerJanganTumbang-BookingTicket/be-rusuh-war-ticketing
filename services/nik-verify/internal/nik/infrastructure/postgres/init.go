// Package postgres adapts the shared Aurora cluster for evidence lookups.
package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
)

// NewPool opens a connection pool and verifies the database answers.
//
// Pool sizing matters more than usual on Lambda: every warm container holds its
// own pool, so a few hundred of them at MaxConns each will exhaust Postgres
// long before the sale peaks. Keep MaxConns small and put RDS Proxy in front —
// see the service README.
func NewPool(ctx context.Context, dsn string, maxConns int32, connLifetime time.Duration) (*pgxpool.Pool, error) {
	return pgutil.NewPool(ctx, dsn, maxConns, connLifetime)
}
