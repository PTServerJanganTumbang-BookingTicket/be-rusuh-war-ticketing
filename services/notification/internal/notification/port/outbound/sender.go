package outbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/domain/entity"
)

// IEmailSender hands a rendered message to the email provider.
//
// Returning an error must leave the message unsent and un-acknowledged, so SQS
// redelivers it and, after maxReceiveCount, parks it on the DLQ (NFR-10).
type IEmailSender interface {
	Send(ctx context.Context, msg entity.Message) error
}
