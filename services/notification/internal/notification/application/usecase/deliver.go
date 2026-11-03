package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/domain/template"
)

// Deliver renders and sends the email for one envelope.
//
// What it returns decides what SQS does next, so the distinction matters:
//
//	nil   — done. Delivered, a duplicate, or unroutable with no prospect of
//	        ever succeeding. The message is deleted from the queue.
//	error — retry. Left on the queue, redelivered, and parked on the DLQ after
//	        maxReceiveCount (NFR-10).
//
// A permanent failure returning an error would be a poison message: the same
// row redelivered until it reaches the DLQ, having wasted every retry.
func (u *NotifierUsecase) Deliver(ctx context.Context, envelope events.Envelope) error {
	if envelope.OutboxID == "" {
		// Without a dedupe key this could be sent repeatedly. Nothing to
		// retry, so drop it and alarm.
		slog.ErrorContext(ctx, "envelope has no outbox id, dropping",
			slog.String("type", string(envelope.Type)))
		return nil
	}

	recipient, rendered, err := u.renderer.Render(envelope)
	if err != nil {
		var noTemplate template.ErrNoTemplate
		if errors.As(err, &noTemplate) {
			// A producer is deployed ahead of this consumer. Retrying will not
			// help until this service ships, and burning retries to reach the
			// DLQ is worse than parking it immediately.
			slog.ErrorContext(ctx, "no template for event type, dropping",
				slog.String("type", string(envelope.Type)),
				slog.String("outbox_id", envelope.OutboxID),
				slog.String("metric", "NotificationUnroutable"))
			return nil
		}
		// A malformed payload is equally permanent — the bytes will not
		// improve on redelivery.
		slog.ErrorContext(ctx, "failed to render notification, dropping",
			slog.String("type", string(envelope.Type)),
			slog.String("outbox_id", envelope.OutboxID),
			slog.Any("err", err),
			slog.String("metric", "NotificationUnroutable"))
		return nil
	}

	if recipient.Email == "" {
		slog.ErrorContext(ctx, "notification has no recipient, dropping",
			slog.String("type", string(envelope.Type)),
			slog.String("outbox_id", envelope.OutboxID),
			slog.String("metric", "NotificationUnroutable"))
		return nil
	}

	// Claim before sending. Claiming after would let two concurrent
	// deliveries of the same message both pass the check and both send.
	fresh, err := u.dedupe.MarkSentIfNew(ctx, envelope.OutboxID, entity.SentTTL)
	if err != nil {
		// Best-effort: a dedupe store outage must not stop mail. The cost is
		// a possible duplicate, which is a nuisance; the alternative is a
		// customer who never receives their ticket.
		slog.WarnContext(ctx, "dedupe store unavailable, sending anyway",
			slog.String("outbox_id", envelope.OutboxID), slog.Any("err", err))
	} else if !fresh {
		slog.InfoContext(ctx, "notification already sent, skipping",
			slog.String("outbox_id", envelope.OutboxID),
			slog.String("metric", "NotificationDeduped"))
		return nil
	}

	msg := entity.Message{
		To:       recipient.Email,
		Subject:  rendered.Subject,
		HTMLBody: rendered.HTMLBody,
		TextBody: rendered.TextBody,
	}

	if err := u.sender.Send(ctx, msg); err != nil {
		// Release the claim, or the redelivery will be mistaken for a
		// duplicate and dropped — the message would be lost precisely because
		// the first attempt failed.
		if unmarkErr := u.dedupe.Unmark(ctx, envelope.OutboxID); unmarkErr != nil {
			slog.ErrorContext(ctx, "failed to release dedupe claim after send failure",
				slog.String("outbox_id", envelope.OutboxID), slog.Any("err", unmarkErr))
		}
		slog.ErrorContext(ctx, "failed to send notification",
			slog.String("type", string(envelope.Type)),
			slog.String("outbox_id", envelope.OutboxID),
			slog.Any("err", err))
		return fmt.Errorf("send %s: %w", envelope.Type, err)
	}

	slog.InfoContext(ctx, "notification sent",
		slog.String("type", string(envelope.Type)),
		slog.String("outbox_id", envelope.OutboxID))
	return nil
}
