package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/port/inbound"
)

// Drain makes one pass over the unpublished rows (ADR-005 §9 step 4).
//
// The shape of a pass:
//
//	claim a batch, locked FOR UPDATE SKIP LOCKED
//	for each row:
//	    take the lease, or skip — someone else has it, or it is already done
//	    publish
//	    on success, remember the id; on failure, release the lease
//	mark the successful ids published, commit
//
// Two ordering decisions carry the correctness:
//
// Publish BEFORE marking published. The reverse loses events: a crash between
// the mark and the publish retires a row nobody ever sent. This way a crash
// leaves the row unpublished and the next pass retries it, which costs a
// duplicate at worst — and the consumer dedupes on outbox_id.
//
// A failed publish does not fail the pass. One malformed row must not block
// every row behind it, so failures are counted and the rest proceed. The row
// stays unpublished and OutboxLag reports it.
//
// Stale rows are dropped, not sent. An event carrying a short-lived token is
// worthless once that token has expired, and delivering it is worse than
// silence: the recipient clicks a login link, is told it is invalid, and now
// has a question. Only types that declare a MaxAge are subject to this; a
// ticket or a payment notice is still worth reading late. See
// pkg/events.Type.MaxAge.
func (u *PublisherUsecase) Drain(ctx context.Context) (portin.Result, error) {
	records, finish, err := u.repo.ClaimUnpublished(ctx, u.batchSize)
	if err != nil {
		return portin.Result{}, err
	}

	result := portin.Result{Claimed: len(records)}
	published := make([]string, 0, len(records))
	// Grouped by reason so one UPDATE covers each distinct cause, and the
	// stored reason stays specific enough to be worth reading.
	dropped := make(map[string][]string)
	now := time.Now()

	for _, record := range records {
		// Before the lease: there is no point claiming a row we will not
		// send, and holding a lease we immediately discard would make a
		// concurrent worker skip a row for no reason.
		if maxAge, bounded := record.Type.MaxAge(); bounded {
			if age := now.Sub(record.CreatedAt); age > maxAge {
				reason := fmt.Sprintf("stale: %s old, max %s", age.Round(time.Second), maxAge)
				slog.WarnContext(ctx, "dropping stale outbox row",
					slog.String("outbox_id", record.ID),
					slog.String("type", string(record.Type)),
					slog.Duration("age", age.Round(time.Second)),
					slog.Duration("max_age", maxAge),
					slog.String("metric", "OutboxDroppedStale"))
				dropped[reason] = append(dropped[reason], record.ID)
				continue
			}
		}

		claimed, err := u.lease.Claim(ctx, record.ID)
		if err != nil {
			// Without a lease two workers could both send this row. Skipping
			// is safe: the row stays unpublished and a later pass retries.
			slog.WarnContext(ctx, "lease unavailable, skipping row",
				slog.String("outbox_id", record.ID), slog.Any("err", err))
			result.Skipped++
			continue
		}
		if !claimed {
			// Another worker holds it, or it is already done.
			result.Skipped++
			continue
		}

		if err := u.publisher.Publish(ctx, record.Envelope()); err != nil {
			slog.ErrorContext(ctx, "failed to publish outbox row",
				slog.String("outbox_id", record.ID),
				slog.String("type", string(record.Type)),
				slog.Any("err", err))
			// Release rather than wait out the lease: the next pass should be
			// able to retry in a second, not in thirty.
			if relErr := u.lease.Release(ctx, record.ID); relErr != nil {
				slog.WarnContext(ctx, "failed to release lease",
					slog.String("outbox_id", record.ID), slog.Any("err", relErr))
			}
			result.Failed++
			continue
		}

		published = append(published, record.ID)
	}

	if len(published) > 0 {
		if err := u.repo.MarkPublished(ctx, published); err != nil {
			// Roll back the claim. The rows were sent, so the consumer will
			// see them again on the next pass and dedupe them — duplicates
			// are survivable, a retired-but-unsent row is not.
			if finishErr := finish(ctx, false); finishErr != nil {
				slog.ErrorContext(ctx, "rollback failed", slog.Any("err", finishErr))
			}
			return result, err
		}
	}

	for reason, ids := range dropped {
		if err := u.repo.MarkDropped(ctx, ids, reason); err != nil {
			// Same reasoning as a failed MarkPublished: roll back rather than
			// leave the transaction's view of these rows ambiguous. Nothing
			// was sent for them, so a retry costs one more evaluation.
			if finishErr := finish(ctx, false); finishErr != nil {
				slog.ErrorContext(ctx, "rollback failed", slog.Any("err", finishErr))
			}
			return result, err
		}
		result.Dropped += len(ids)
	}

	if err := finish(ctx, true); err != nil {
		return result, err
	}

	result.Published = len(published)
	for _, id := range published {
		if err := u.lease.Complete(ctx, id); err != nil {
			// The row is committed as published, so this only costs the
			// dedupe marker — a redelivery would be reprocessed rather than
			// recognised. Not worth failing the pass.
			slog.WarnContext(ctx, "failed to complete lease",
				slog.String("outbox_id", id), slog.Any("err", err))
		}
	}

	if remaining, err := u.repo.CountUnpublished(ctx); err == nil {
		result.Remaining = remaining
	}
	return result, nil
}
