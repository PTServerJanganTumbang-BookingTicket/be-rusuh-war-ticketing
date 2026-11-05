package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	mock_outbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/_mock/notification/port/outbound"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/domain/template"
)

type deps struct {
	sender *mock_outbound.MockIEmailSender
	dedupe *mock_outbound.MockIDedupeStore
}

func newTestUsecase(t *testing.T) (*NotifierUsecase, deps) {
	t.Helper()
	ctrl := gomock.NewController(t)
	d := deps{
		sender: mock_outbound.NewMockIEmailSender(ctrl),
		dedupe: mock_outbound.NewMockIDedupeStore(ctrl),
	}
	renderer := template.NewRenderer(template.Links{
		AppBase: "https://rusuh.id", MagicTokenTTLMinutes: 15,
	})
	uc := NewNotifier(renderer, d.sender, d.dedupe).(*NotifierUsecase)
	return uc, d
}

func envelope(t *testing.T, id string, eventType events.Type, payload any) events.Envelope {
	t.Helper()
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	return events.Envelope{OutboxID: id, AggregateID: "agg-1", Type: eventType, Payload: encoded}
}

func magicLink(t *testing.T, id string) events.Envelope {
	return envelope(t, id, events.TypeAuthMagicLink, events.AuthMagicLinkPayload{
		Email: "user@example.com", Token: "tok-123",
	})
}

func TestDeliver_SendsAndDedupes(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.dedupe.EXPECT().MarkSentIfNew(gomock.Any(), "evt-1", entity.SentTTL).Return(true, nil)

	var sent entity.Message
	d.sender.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, m entity.Message) error { sent = m; return nil })

	require.NoError(t, uc.Deliver(context.Background(), magicLink(t, "evt-1")))
	assert.Equal(t, "user@example.com", sent.To)
	assert.NotEmpty(t, sent.Subject)
	assert.Contains(t, sent.HTMLBody, "tok-123")
	assert.Contains(t, sent.TextBody, "tok-123")
}

func TestDeliver_ClaimsBeforeSending(t *testing.T) {
	// Claiming after would let two concurrent deliveries both pass the check
	// and both send.
	uc, d := newTestUsecase(t)
	claimed := d.dedupe.EXPECT().MarkSentIfNew(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	d.sender.EXPECT().Send(gomock.Any(), gomock.Any()).Return(nil).After(claimed)

	require.NoError(t, uc.Deliver(context.Background(), magicLink(t, "evt-1")))
}

func TestDeliver_DuplicateIsSkipped(t *testing.T) {
	// No Send expectation: a second delivery of the same row must not email
	// the customer twice.
	uc, d := newTestUsecase(t)
	d.dedupe.EXPECT().MarkSentIfNew(gomock.Any(), "evt-1", gomock.Any()).Return(false, nil)

	require.NoError(t, uc.Deliver(context.Background(), magicLink(t, "evt-1")))
}

func TestDeliver_DedupeOutageStillSends(t *testing.T) {
	// A duplicate email is a nuisance; a missing e-ticket is a customer who
	// cannot get into the venue. So this fails toward sending.
	uc, d := newTestUsecase(t)
	d.dedupe.EXPECT().MarkSentIfNew(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(false, errors.New("redis down"))
	d.sender.EXPECT().Send(gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, uc.Deliver(context.Background(), magicLink(t, "evt-1")))
}

func TestDeliver_SendFailureReleasesTheClaimAndRetries(t *testing.T) {
	// Without the release, the redelivery would look like a duplicate and be
	// dropped — the message lost precisely because the first attempt failed.
	uc, d := newTestUsecase(t)
	d.dedupe.EXPECT().MarkSentIfNew(gomock.Any(), "evt-1", gomock.Any()).Return(true, nil)
	d.sender.EXPECT().Send(gomock.Any(), gomock.Any()).Return(errors.New("ses throttled"))
	d.dedupe.EXPECT().Unmark(gomock.Any(), "evt-1").Return(nil)

	err := uc.Deliver(context.Background(), magicLink(t, "evt-1"))
	assert.Error(t, err, "an error leaves it on the queue for retry, then the DLQ")
}

func TestDeliver_UnknownTypeIsDroppedNotRetried(t *testing.T) {
	// A producer deployed ahead of this consumer. Retrying cannot help until
	// this service ships, and burning retries to reach the DLQ is worse than
	// parking it now.
	uc, _ := newTestUsecase(t)
	env := events.Envelope{OutboxID: "evt-1", Type: events.Type("payment.refund"), Payload: []byte(`{}`)}

	require.NoError(t, uc.Deliver(context.Background(), env), "nil deletes it from the queue")
}

func TestDeliver_MalformedPayloadIsDropped(t *testing.T) {
	// The bytes will not improve on redelivery.
	uc, _ := newTestUsecase(t)
	env := events.Envelope{OutboxID: "evt-1", Type: events.TypeAuthMagicLink, Payload: []byte(`{not json`)}

	require.NoError(t, uc.Deliver(context.Background(), env))
}

func TestDeliver_MissingRecipientIsDropped(t *testing.T) {
	uc, _ := newTestUsecase(t)
	env := envelope(t, "evt-1", events.TypeAuthMagicLink, events.AuthMagicLinkPayload{Token: "tok"})

	require.NoError(t, uc.Deliver(context.Background(), env))
}

func TestDeliver_MissingOutboxIDIsDropped(t *testing.T) {
	// Without a dedupe key this could be sent repeatedly.
	uc, _ := newTestUsecase(t)
	env := magicLink(t, "")

	require.NoError(t, uc.Deliver(context.Background(), env))
}

func TestDeliver_TicketGoesToTheHolderNotTheBuyer(t *testing.T) {
	// FR-26: one email per NIK holder.
	uc, d := newTestUsecase(t)
	env := envelope(t, "evt-1", events.TypeTicketIssue, events.TicketIssuePayload{
		TicketID: "TKT-001", BookingID: "RB-00912",
		HolderEmail: "friend@example.com",
		EventName:   "Konser A", EventDate: "2026-12-25", EventVenue: "GBK",
		Category: "VIP", NIKMasked: "3171**********22", QRValue: "TKT-001.nonce",
	})
	d.dedupe.EXPECT().MarkSentIfNew(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)

	var sent entity.Message
	d.sender.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, m entity.Message) error { sent = m; return nil })

	require.NoError(t, uc.Deliver(context.Background(), env))
	assert.Equal(t, "friend@example.com", sent.To)
	assert.Contains(t, sent.Subject, "Konser A")
	assert.Contains(t, sent.HTMLBody, "3171**********22")
	assert.NotContains(t, sent.HTMLBody, "TKT-001.nonce", "the QR value is not in the email body")
}
