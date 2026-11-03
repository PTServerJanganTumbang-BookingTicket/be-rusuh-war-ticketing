// Package pgutil provides shared PostgreSQL connection helpers.
package pgutil

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a connection pool and verifies the database answers.
//
// MaxConns is deliberately small. Every warm Lambda container holds its own
// pool, so a few hundred of them at a generous MaxConns will exhaust Postgres
// long before the sale peaks — put RDS Proxy in front and keep this low.
func NewPool(ctx context.Context, dsn string, maxConns int32, connLifetime time.Duration) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DSN: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	if connLifetime > 0 {
		cfg.MaxConnLifetime = connLifetime
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}
