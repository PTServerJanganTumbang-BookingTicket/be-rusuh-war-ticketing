package outbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/domain/entity"
)

// IOutboxRepository reads and retires outbox rows.
type IOutboxRepository interface {
	// ClaimUnpublished returns up to limit unpublished rows, oldest first,
	// locked FOR UPDATE SKIP LOCKED so concurrent publishers take disjoint
	// work rather than queueing behind each other.
	//
	// The returned function commits or rolls back the surrounding transaction;
	// callers must always invoke it.
	ClaimUnpublished(ctx context.Context, limit int) ([]entity.Record, func(context.Context, bool) error, error)

	// MarkPublished sets published_at. Called inside the claim's transaction,
	// so a row is only retired if the whole pass commits.
	MarkPublished(ctx context.Context, ids []string) error

	// CountUnpublished is the OutboxLag metric (ADR-005 §observability).
	CountUnpublished(ctx context.Context) (int, error)
}
