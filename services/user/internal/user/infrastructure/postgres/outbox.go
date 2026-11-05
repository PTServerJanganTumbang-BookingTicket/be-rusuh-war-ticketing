package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

// insertOutbox is the one statement every producer in this service uses.
//
// It is always executed on a transaction handle, never on the pool: an outbox
// row that commits without the state change it describes is an email about
// something that never happened (ADR-005 §9).
const insertOutbox = `
INSERT INTO outbox (id, aggregate_id, type, payload, created_at)
VALUES ($1, $2, $3, $4, $5)
`

// enqueue writes an outbox row inside the caller's transaction.
func enqueue(ctx context.Context, tx pgx.Tx, e entity.OutboxEvent) error {
	_, err := tx.Exec(ctx, insertOutbox, e.ID, e.AggregateID, string(e.Type), []byte(e.Payload), e.CreatedAt)
	return err
}
