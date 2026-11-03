// Package postgres reads the outbox from the shared Aurora cluster.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/port/outbound"
)

// NewPool opens a connection pool and verifies the database answers.
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

// OutboxRepository reads and retires outbox rows.
//
// It writes only published_at. The rows themselves belong to whichever service
// produced them.
type OutboxRepository struct {
	pool DBConn
}

// NewOutboxRepository returns the outbox adapter.
func NewOutboxRepository(pool DBConn) portout.IOutboxRepository {
	return &OutboxRepository{pool: pool}
}

// claimUnpublished is ADR-005 §9 step 4's query.
//
// SKIP LOCKED is what makes more than one publisher useful: without it a
// second worker blocks on the first worker's locks and adds nothing. With it,
// each pass takes a disjoint slice of the backlog.
//
// ORDER BY created_at, id keeps delivery roughly causal and makes the ordering
// total — created_at alone ties on rows written in the same transaction, and a
// non-deterministic order would make the backlog's progress unpredictable.
const claimUnpublished = `
SELECT id, aggregate_id, type, payload, created_at
  FROM outbox
 WHERE published_at IS NULL
 ORDER BY created_at, id
 FOR UPDATE SKIP LOCKED
 LIMIT $1
`

// ClaimUnpublished locks a batch and returns it with a commit/rollback handle.
//
// The transaction stays open until the caller calls finish, because the row
// locks are what stop a concurrent publisher taking the same work. The caller
// must always call it.
func (r *OutboxRepository) ClaimUnpublished(
	ctx context.Context, limit int,
) ([]entity.Record, func(context.Context, bool) error, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("outbox: begin: %w", err)
	}

	finish := func(ctx context.Context, commit bool) error {
		if commit {
			return tx.Commit(ctx)
		}
		return tx.Rollback(ctx)
	}

	rows, err := tx.Query(ctx, claimUnpublished, limit)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, fmt.Errorf("outbox: claim: %w", err)
	}

	records := make([]entity.Record, 0, limit)
	for rows.Next() {
		var rec entity.Record
		var eventType string
		var payload []byte
		if err := rows.Scan(&rec.ID, &rec.AggregateID, &eventType, &payload, &rec.CreatedAt); err != nil {
			rows.Close()
			_ = tx.Rollback(ctx)
			return nil, nil, fmt.Errorf("outbox: scan: %w", err)
		}
		rec.Type = events.Type(eventType)
		rec.Payload = payload
		records = append(records, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, fmt.Errorf("outbox: iterate: %w", err)
	}

	// The transaction must be closed by the caller even when the batch is
	// empty, or its snapshot pins the oldest transaction id and bloats the
	// table it is reading.
	return records, finish, nil
}

// MarkPublished retires rows, on the pool rather than the claim's transaction.
//
// A separate connection is safe here because the ids being marked are exactly
// the rows this worker holds locks on, and no other writer touches
// published_at.
func (r *OutboxRepository) MarkPublished(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("outbox: mark published: %w", err)
	}
	return nil
}

// CountUnpublished is the OutboxLag metric.
//
// Served by the partial index on published_at IS NULL, so it costs the size of
// the backlog rather than the size of all history.
func (r *OutboxRepository) CountUnpublished(ctx context.Context) (int, error) {
	var count int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&count); err != nil {
		return 0, fmt.Errorf("outbox: count: %w", err)
	}
	return count, nil
}
