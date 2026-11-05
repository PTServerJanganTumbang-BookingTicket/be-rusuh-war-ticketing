// Package sqs publishes outbox envelopes to the notification queue.
package sqs

import (
	"context"
	"encoding/json"
	"fmt"

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
	client   API
	queueURL string
}

// NewPublisher returns the SQS adapter.
func NewPublisher(client API, queueURL string) portout.IEventPublisher {
	return &Publisher{client: client, queueURL: queueURL}
}

// Publish sends one envelope.
//
// MessageGroupId and MessageDeduplicationId are set from the aggregate and the
// outbox id, which matter only on a FIFO queue and are harmless on a standard
// one. On FIFO they give per-aggregate ordering — two events about one booking
// arrive in order — while letting unrelated aggregates flow in parallel.
func (p *Publisher) Publish(ctx context.Context, envelope events.Envelope) error {
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("sqs: encode envelope: %w", err)
	}

	_, err = p.client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:               &p.queueURL,
		MessageBody:            strPtr(string(body)),
		MessageGroupId:         strPtr(envelope.AggregateID),
		MessageDeduplicationId: strPtr(envelope.OutboxID),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"type": {DataType: strPtr("String"), StringValue: strPtr(string(envelope.Type))},
		},
	})
	if err != nil {
		return fmt.Errorf("sqs: send: %w", err)
	}
	return nil
}

func strPtr(s string) *string { return &s }
