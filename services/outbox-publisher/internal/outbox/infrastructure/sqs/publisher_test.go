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
	// MessageAttributes must carry the event type
	typeAttr, ok := captured.MessageAttributes["type"]
	require.True(t, ok)
	assert.Equal(t, string(events.TypeAccountVerify), *typeAttr.StringValue)
}

// ── FIFO-only parameters ──────────────────────────────────────────────────────
//
// SQS rejects MessageGroupId and MessageDeduplicationId on a standard queue
// with InvalidParameterValue - it does not ignore them. Sending them
// unconditionally made every publish fail against the standard queue this
// system deploys, so this distinction is load-bearing, not cosmetic.

func TestPublish_StandardQueue_OmitsFifoParameters(t *testing.T) {
	var captured *awssqs.SendMessageInput
	stub := &stubSQS{fn: func(_ context.Context, in *awssqs.SendMessageInput, _ ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error) {
		captured = in
		return &awssqs.SendMessageOutput{}, nil
	}}
	publisher := NewPublisher(stub, "https://sqs.ap-southeast-1.amazonaws.com/123/rusuh-prod-notification")
	err := publisher.Publish(context.Background(), events.Envelope{
		OutboxID: "ob-1", AggregateID: "agg-1", Type: events.TypeAccountVerify,
	})
	require.NoError(t, err)
	assert.Nil(t, captured.MessageGroupId)
	assert.Nil(t, captured.MessageDeduplicationId)
}

func TestPublish_FifoQueue_SetsFifoParameters(t *testing.T) {
	var captured *awssqs.SendMessageInput
	stub := &stubSQS{fn: func(_ context.Context, in *awssqs.SendMessageInput, _ ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error) {
		captured = in
		return &awssqs.SendMessageOutput{}, nil
	}}
	publisher := NewPublisher(stub, "https://sqs.ap-southeast-1.amazonaws.com/123/rusuh-prod-notification.fifo")
	err := publisher.Publish(context.Background(), events.Envelope{
		OutboxID: "ob-1", AggregateID: "agg-1", Type: events.TypeAccountVerify,
	})
	require.NoError(t, err)
	require.NotNil(t, captured.MessageGroupId)
	require.NotNil(t, captured.MessageDeduplicationId)
	assert.Equal(t, "agg-1", *captured.MessageGroupId)
	assert.Equal(t, "ob-1", *captured.MessageDeduplicationId)
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
