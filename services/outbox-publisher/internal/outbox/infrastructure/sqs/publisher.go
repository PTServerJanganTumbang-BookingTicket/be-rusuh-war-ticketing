// Package sqs publishes outbox envelopes to the notification queue.
package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/port/outbound"
)

// API is the slice of the SQS client this adapter uses, so tests can
// substitute it without the AWS SDK.
type API interface {
	SendMessage(ctx context.Context, in *awssqs.SendMessageInput, opts ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error)
}

// Publisher sends envelopes to one queue.
type Publisher struct {
	client API
	// fifo is derived from the queue URL once, at construction. FIFO queue
	// names must end in ".fifo", so the URL is authoritative and costs no
	// API call.
	fifo     bool
	queueURL string
}

// NewPublisher returns the SQS adapter.
func NewPublisher(client API, queueURL string) portout.IEventPublisher {
	return &Publisher{
		client:   client,
		queueURL: queueURL,
		fifo:     strings.HasSuffix(queueURL, ".fifo"),
	}
}

// Publish sends one envelope.
//
// MessageGroupId and MessageDeduplicationId are sent ONLY to a FIFO queue.
// They are not merely ignored on a standard queue - SQS rejects the whole
// request:
//
//	InvalidParameterValue: Value <id> for parameter MessageDeduplicationId is
//	invalid. Reason: The request include parameter that is not valid for this
//	queue type.
//
// Sending them unconditionally made every publish fail against the standard
// queue this system deploys, while the HTTP call that wrote the outbox row
// still returned 200 - the row commits first and the send happens after. The
// only visible symptom was an email that never arrived.
//
// On FIFO they give per-aggregate ordering: two events about one booking
// arrive in order, while unrelated aggregates flow in parallel. Losing that
// on a standard queue is acceptable here because the consumer deduplicates
// through its own store (IDedupeStore) rather than relying on the broker.
func (p *Publisher) Publish(ctx context.Context, envelope events.Envelope) error {
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("sqs: encode envelope: %w", err)
	}

	in := &awssqs.SendMessageInput{
		QueueUrl:    &p.queueURL,
		MessageBody: strPtr(string(body)),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"type": {DataType: strPtr("String"), StringValue: strPtr(string(envelope.Type))},
		},
	}
	if p.fifo {
		in.MessageGroupId = strPtr(envelope.AggregateID)
		in.MessageDeduplicationId = strPtr(envelope.OutboxID)
	}

	_, err = p.client.SendMessage(ctx, in)
	if err != nil {
		return fmt.Errorf("sqs: send: %w", err)
	}
	return nil
}

func strPtr(s string) *string { return &s }
