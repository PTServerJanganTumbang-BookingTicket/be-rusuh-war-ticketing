package sqs

import (
	"context"
	"errors"
	"testing"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

// ── API stub ──────────────────────────────────────────────────────────────────

type stubSQS struct {
	fn func(ctx context.Context, in *awssqs.SendMessageInput, opts ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error)
}

func (s *stubSQS) SendMessage(ctx context.Context, in *awssqs.SendMessageInput, opts ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error) {
	return s.fn(ctx, in, opts...)
}

// ── tests ──────────────────────────────────────────────────────────────────────

func TestPublish_OK(t *testing.T) {
	var captured *awssqs.SendMessageInput
	stub := &stubSQS{fn: func(_ context.Context, in *awssqs.SendMessageInput, _ ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error) {
		captured = in
		return &awssqs.SendMessageOutput{}, nil
	}}
	publisher := NewPublisher(stub, "https://sqs.example.com/queue")
	env := events.Envelope{
		OutboxID:    "ob-1",
		AggregateID: "agg-1",
		Type:        events.TypeAccountVerify,
		Payload:     []byte(`{}`),
	}
	err := publisher.Publish(context.Background(), env)
	require.NoError(t, err)
	assert.Equal(t, "https://sqs.example.com/queue", *captured.QueueUrl)
	assert.Equal(t, "agg-1", *captured.MessageGroupId)
	assert.Equal(t, "ob-1", *captured.MessageDeduplicationId)
	// MessageAttributes must carry the event type
	typeAttr, ok := captured.MessageAttributes["type"]
	require.True(t, ok)
	assert.Equal(t, string(events.TypeAccountVerify), *typeAttr.StringValue)
}

func TestPublish_SendError(t *testing.T) {
	stub := &stubSQS{fn: func(_ context.Context, _ *awssqs.SendMessageInput, _ ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error) {
		return nil, errors.New("sqs: network error")
	}}
	publisher := NewPublisher(stub, "https://sqs.example.com/queue")
	err := publisher.Publish(context.Background(), events.Envelope{AggregateID: "a", OutboxID: "o"})
	assert.Error(t, err)
}

func TestStrPtr(t *testing.T) {
	s := "hello"
	assert.Equal(t, &s, strPtr("hello"))
}
