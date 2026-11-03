// Package sqsconsumer adapts Lambda's SQS event into use case calls.
package sqsconsumer

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-lambda-go/events"

	rusuhevents "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/port/inbound"
)

// Handler processes an SQS batch.
type Handler struct {
	usecase portin.INotifierUsecase
}

// NewHandler returns the SQS adapter.
func NewHandler(uc portin.INotifierUsecase) *Handler {
	return &Handler{usecase: uc}
}

// Handle processes a batch and reports per-message failures.
//
// Partial batch failure is the point of the return value: without it, one
// failing message in a batch of ten redelivers all ten, so nine customers get
// a duplicate email because of the tenth. Reporting only the failed
// identifiers requires ReportBatchItemFailures on the event source mapping —
// see template.yaml.
func (h *Handler) Handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	var failures []events.SQSBatchItemFailure

	for _, record := range event.Records {
		var envelope rusuhevents.Envelope
		if err := json.Unmarshal([]byte(record.Body), &envelope); err != nil {
			// Unparseable: the bytes will not improve on redelivery, so do
			// not report it as a failure and burn retries reaching the DLQ.
			slog.ErrorContext(ctx, "unparseable queue message, dropping",
				slog.String("message_id", record.MessageId), slog.Any("err", err))
			continue
		}

		if err := h.usecase.Deliver(ctx, envelope); err != nil {
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: record.MessageId})
		}
	}

	// The error is nil even with failures: returning one would fail the whole
	// batch and discard the per-item report.
	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}
