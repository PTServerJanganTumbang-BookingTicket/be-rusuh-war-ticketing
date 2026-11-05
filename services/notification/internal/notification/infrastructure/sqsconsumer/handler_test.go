package sqsconsumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	awsevents "github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	rusuhevents "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	mock_inbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/_mock/notification/port/inbound"
)

func newTestHandler(t *testing.T) (*Handler, *mock_inbound.MockINotifierUsecase) {
	t.Helper()
	uc := mock_inbound.NewMockINotifierUsecase(gomock.NewController(t))
	return NewHandler(uc), uc
}

func sqsRecord(t *testing.T, messageID, outboxID string) awsevents.SQSMessage {
	t.Helper()
	payload, err := json.Marshal(rusuhevents.AuthMagicLinkPayload{Email: "a@example.com", Token: "t"})
	require.NoError(t, err)
	body, err := json.Marshal(rusuhevents.Envelope{
		OutboxID: outboxID, Type: rusuhevents.TypeAuthMagicLink, Payload: payload,
	})
	require.NoError(t, err)
	return awsevents.SQSMessage{MessageId: messageID, Body: string(body)}
}

func TestHandle_AllSucceed(t *testing.T) {
	h, uc := newTestHandler(t)
	uc.EXPECT().Deliver(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	resp, err := h.Handle(context.Background(), awsevents.SQSEvent{Records: []awsevents.SQSMessage{
		sqsRecord(t, "m1", "evt-1"), sqsRecord(t, "m2", "evt-2"),
	}})
	require.NoError(t, err)
	assert.Empty(t, resp.BatchItemFailures)
}

func TestHandle_ReportsOnlyTheFailedMessage(t *testing.T) {
	// Without partial batch failure, one bad message redelivers all ten and
	// nine customers get a duplicate email.
	h, uc := newTestHandler(t)
	uc.EXPECT().Deliver(gomock.Any(), gomock.Any()).Return(nil)
	uc.EXPECT().Deliver(gomock.Any(), gomock.Any()).Return(errors.New("ses down"))
	uc.EXPECT().Deliver(gomock.Any(), gomock.Any()).Return(nil)

	resp, err := h.Handle(context.Background(), awsevents.SQSEvent{Records: []awsevents.SQSMessage{
		sqsRecord(t, "m1", "evt-1"), sqsRecord(t, "m2", "evt-2"), sqsRecord(t, "m3", "evt-3"),
	}})
	require.NoError(t, err, "returning an error would fail the whole batch and discard the report")
	require.Len(t, resp.BatchItemFailures, 1)
	assert.Equal(t, "m2", resp.BatchItemFailures[0].ItemIdentifier)
}

func TestHandle_UnparseableBodyIsDroppedNotRetried(t *testing.T) {
	// No Deliver expectation: the bytes will not improve on redelivery, so
	// reporting it would just burn retries on the way to the DLQ.
	h, _ := newTestHandler(t)

	resp, err := h.Handle(context.Background(), awsevents.SQSEvent{Records: []awsevents.SQSMessage{
		{MessageId: "m1", Body: `{not json`},
	}})
	require.NoError(t, err)
	assert.Empty(t, resp.BatchItemFailures)
}

func TestHandle_EmptyBatch(t *testing.T) {
	h, _ := newTestHandler(t)
	resp, err := h.Handle(context.Background(), awsevents.SQSEvent{})
	require.NoError(t, err)
	assert.Empty(t, resp.BatchItemFailures)
}

func TestHandle_PassesTheEnvelopeThrough(t *testing.T) {
	h, uc := newTestHandler(t)

	var got rusuhevents.Envelope
	uc.EXPECT().Deliver(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e rusuhevents.Envelope) error { got = e; return nil })

	_, err := h.Handle(context.Background(), awsevents.SQSEvent{Records: []awsevents.SQSMessage{
		sqsRecord(t, "m1", "evt-42"),
	}})
	require.NoError(t, err)
	assert.Equal(t, "evt-42", got.OutboxID)
	assert.Equal(t, rusuhevents.TypeAuthMagicLink, got.Type)
}
