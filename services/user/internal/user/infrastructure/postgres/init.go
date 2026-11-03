// Package postgres is the Aurora-backed implementation of IUserRepository.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres SQLSTATE codes this adapter interprets.
const (
	codeUniqueViolation = "23505"
	codeCheckViolation  = "23514"
)

// Index names from migrations/0001_user_domain.up.sql. The adapter maps a
// unique violation to the right contract error by index name, so the caller
// learns WHICH uniqueness failed rather than a generic conflict.
const (
	indexAccountEmail   = "accounts_email_key"
	indexAccountNIK     = "accounts_nik_hmac_key"
	indexSavedNIKPerAcc = "saved_niks_account_nik_key"
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

// uniqueViolation reports the constraint name when err is a unique violation.
func uniqueViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == codeUniqueViolation {
		return pgErr.ConstraintName, true
	}
	return "", false
}

// checkViolation reports the constraint name when err is a CHECK violation.
func checkViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == codeCheckViolation {
		return pgErr.ConstraintName, true
	}
	return "", false
}
