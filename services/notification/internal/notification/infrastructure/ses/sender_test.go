package ses

import (
	"context"
	"errors"
	"testing"

	awsses "github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/domain/entity"
)

// ── API stub ──────────────────────────────────────────────────────────────────

type stubSES struct {
	fn func(ctx context.Context, in *awsses.SendEmailInput, opts ...func(*awsses.Options)) (*awsses.SendEmailOutput, error)
}

func (s *stubSES) SendEmail(ctx context.Context, in *awsses.SendEmailInput, opts ...func(*awsses.Options)) (*awsses.SendEmailOutput, error) {
	return s.fn(ctx, in, opts...)
}

// ── tests ──────────────────────────────────────────────────────────────────────

func TestSend_OK(t *testing.T) {
	var captured *awsses.SendEmailInput
	stub := &stubSES{fn: func(_ context.Context, in *awsses.SendEmailInput, _ ...func(*awsses.Options)) (*awsses.SendEmailOutput, error) {
		captured = in
		return &awsses.SendEmailOutput{}, nil
	}}
	sender := NewSender(stub, "from@example.com", "my-config-set")
	err := sender.Send(context.Background(), entity.Message{
		To:       "to@example.com",
		Subject:  "Hello",
		HTMLBody: "<b>Hi</b>",
		TextBody: "Hi",
	})
	require.NoError(t, err)
	assert.Equal(t, "from@example.com", *captured.FromEmailAddress)
	assert.Equal(t, "to@example.com", captured.Destination.ToAddresses[0])
	assert.Equal(t, "my-config-set", *captured.ConfigurationSetName)
}

func TestSend_NoConfigurationSet(t *testing.T) {
	var captured *awsses.SendEmailInput
	stub := &stubSES{fn: func(_ context.Context, in *awsses.SendEmailInput, _ ...func(*awsses.Options)) (*awsses.SendEmailOutput, error) {
		captured = in
		return &awsses.SendEmailOutput{}, nil
	}}
	sender := NewSender(stub, "from@example.com", "")
	require.NoError(t, sender.Send(context.Background(), entity.Message{To: "to@x.com"}))
	assert.Nil(t, captured.ConfigurationSetName)
}

func TestSend_APIError(t *testing.T) {
	stub := &stubSES{fn: func(_ context.Context, _ *awsses.SendEmailInput, _ ...func(*awsses.Options)) (*awsses.SendEmailOutput, error) {
		return nil, errors.New("ses: throttled")
	}}
	sender := NewSender(stub, "from@example.com", "cfg")
	err := sender.Send(context.Background(), entity.Message{To: "to@x.com"})
	assert.Error(t, err)
}
