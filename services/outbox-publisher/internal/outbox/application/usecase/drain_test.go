package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	mock_outbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/_mock/outbox/port/outbound"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/domain/entity"
)

type deps struct {
	repo      *mock_outbound.MockIOutboxRepository
	lease     *mock_outbound.MockILeaseGuard
	publisher *mock_outbound.MockIEventPublisher
}

func newTestUsecase(t *testing.T) (*PublisherUsecase, deps) {
	t.Helper()
	ctrl := gomock.NewController(t)
	d := deps{
		repo:      mock_outbound.NewMockIOutboxRepository(ctrl),
		lease:     mock_outbound.NewMockILeaseGuard(ctrl),
		publisher: mock_outbound.NewMockIEventPublisher(ctrl),
	}
	uc := NewPublisher(d.repo, d.lease, d.publisher, entity.BatchSize).(*PublisherUsecase)
	return uc, d
}

func record(id string) entity.Record {
	payload, _ := json.Marshal(events.AccountVerifyPayload{
		AccountID: "USR-001", Email: "a@example.com", Token: "tok",
	})
	return entity.Record{
		ID:          id,
		AggregateID: "USR-001",
		Type:        events.TypeAccountVerify,
		Payload:     payload,
		CreatedAt:   time.Now().UTC(),
	}
}

// finisher records how the claim transaction was closed.
type finisher struct {
	called    bool
	committed bool
}

func (f *finisher) fn() func(context.Context, bool) error {
	return func(_ context.Context, commit bool) error {
		f.called = true
		f.committed = commit
		return nil
	}
}

func TestDrain_PublishesAndRetires(t *testing.T) {
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), entity.BatchSize).
		Return([]entity.Record{record("evt-1"), record("evt-2")}, fin.fn(), nil)
	d.lease.EXPECT().Claim(gomock.Any(), "evt-1").Return(true, nil)
	d.lease.EXPECT().Claim(gomock.Any(), "evt-2").Return(true, nil)
	d.publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	d.repo.EXPECT().MarkPublished(gomock.Any(), []string{"evt-1", "evt-2"}).Return(nil)
	d.lease.EXPECT().Complete(gomock.Any(), "evt-1").Return(nil)
	d.lease.EXPECT().Complete(gomock.Any(), "evt-2").Return(nil)
	d.repo.EXPECT().CountUnpublished(gomock.Any()).Return(0, nil)

	result, err := uc.Drain(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, result.Claimed)
	assert.Equal(t, 2, result.Published)
	assert.True(t, fin.called && fin.committed)
}

func TestDrain_PublishesBeforeMarking(t *testing.T) {
	// Ordering is the correctness property: marking first and crashing before
	// the publish retires a row nobody ever sent. gomock's After enforces it.
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return([]entity.Record{record("evt-1")}, fin.fn(), nil)
	d.lease.EXPECT().Claim(gomock.Any(), "evt-1").Return(true, nil)
	published := d.publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil)
	d.repo.EXPECT().MarkPublished(gomock.Any(), gomock.Any()).Return(nil).After(published)
	d.lease.EXPECT().Complete(gomock.Any(), "evt-1").Return(nil)
	d.repo.EXPECT().CountUnpublished(gomock.Any()).Return(0, nil)

	_, err := uc.Drain(context.Background())
	require.NoError(t, err)
}

func TestDrain_CarriesTheOutboxIDForConsumerDedupe(t *testing.T) {
	// SQS is at-least-once and the lease only stops concurrent duplication,
	// so the consumer needs a stable key of its own.
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return([]entity.Record{record("evt-1")}, fin.fn(), nil)
	d.lease.EXPECT().Claim(gomock.Any(), gomock.Any()).Return(true, nil)

	var sent events.Envelope
	d.publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e events.Envelope) error { sent = e; return nil })
	d.repo.EXPECT().MarkPublished(gomock.Any(), gomock.Any()).Return(nil)
	d.lease.EXPECT().Complete(gomock.Any(), gomock.Any()).Return(nil)
	d.repo.EXPECT().CountUnpublished(gomock.Any()).Return(0, nil)

	_, err := uc.Drain(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "evt-1", sent.OutboxID)
	assert.Equal(t, events.TypeAccountVerify, sent.Type)
	assert.Equal(t, "USR-001", sent.AggregateID)
}

func TestDrain_SkipsRowsHeldElsewhere(t *testing.T) {
	// payment-api claims the same rows on its inline fast path. A row it holds
	// must not be sent twice.
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return([]entity.Record{record("evt-1")}, fin.fn(), nil)
	d.lease.EXPECT().Claim(gomock.Any(), "evt-1").Return(false, nil)
	d.repo.EXPECT().CountUnpublished(gomock.Any()).Return(1, nil)

	result, err := uc.Drain(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, 0, result.Published)
	assert.True(t, fin.committed, "nothing was published, but the claim still has to close")
}

func TestDrain_LeaseErrorSkipsRatherThanDuplicating(t *testing.T) {
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return([]entity.Record{record("evt-1")}, fin.fn(), nil)
	d.lease.EXPECT().Claim(gomock.Any(), "evt-1").Return(false, errors.New("redis down"))
	d.repo.EXPECT().CountUnpublished(gomock.Any()).Return(1, nil)

	result, err := uc.Drain(context.Background())
	require.NoError(t, err, "a lease outage must not fail the pass")
	assert.Equal(t, 1, result.Skipped)
}

func TestDrain_FailedPublishReleasesTheLeaseAndKeepsGoing(t *testing.T) {
	// One malformed row must not block every row behind it.
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return([]entity.Record{record("evt-bad"), record("evt-good")}, fin.fn(), nil)
	d.lease.EXPECT().Claim(gomock.Any(), "evt-bad").Return(true, nil)
	d.lease.EXPECT().Claim(gomock.Any(), "evt-good").Return(true, nil)
	d.publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(errors.New("sqs down"))
	d.publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil)
	// Released, not left to expire: the next pass should retry in a second.
	d.lease.EXPECT().Release(gomock.Any(), "evt-bad").Return(nil)
	d.repo.EXPECT().MarkPublished(gomock.Any(), []string{"evt-good"}).Return(nil)
	d.lease.EXPECT().Complete(gomock.Any(), "evt-good").Return(nil)
	d.repo.EXPECT().CountUnpublished(gomock.Any()).Return(1, nil)

	result, err := uc.Drain(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, 1, result.Published)
	assert.Equal(t, 1, result.Remaining)
}

func TestDrain_MarkFailureRollsBackRatherThanRetiring(t *testing.T) {
	// The rows were sent. Committing a failed mark is fine; what must not
	// happen is retiring rows whose mark failed, so the claim rolls back and
	// the consumer dedupes the redelivery.
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return([]entity.Record{record("evt-1")}, fin.fn(), nil)
	d.lease.EXPECT().Claim(gomock.Any(), gomock.Any()).Return(true, nil)
	d.publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil)
	d.repo.EXPECT().MarkPublished(gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	_, err := uc.Drain(context.Background())
	require.Error(t, err)
	assert.True(t, fin.called)
	assert.False(t, fin.committed, "the claim must roll back")
}

func TestDrain_EmptyOutboxStillClosesTheClaim(t *testing.T) {
	// An open transaction pins the oldest transaction id and bloats the table
	// it is reading, so even a no-op pass must close.
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return([]entity.Record{}, fin.fn(), nil)
	d.repo.EXPECT().CountUnpublished(gomock.Any()).Return(0, nil)

	result, err := uc.Drain(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, result.Claimed)
	assert.True(t, fin.called && fin.committed)
}

func TestDrain_ClaimFailurePropagates(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("db down"))

	_, err := uc.Drain(context.Background())
	assert.Error(t, err)
}

func TestDrain_CompleteFailureDoesNotFailThePass(t *testing.T) {
	// The row is already committed as published. A lost dedupe marker only
	// means a redelivery would be reprocessed.
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return([]entity.Record{record("evt-1")}, fin.fn(), nil)
	d.lease.EXPECT().Claim(gomock.Any(), gomock.Any()).Return(true, nil)
	d.publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil)
	d.repo.EXPECT().MarkPublished(gomock.Any(), gomock.Any()).Return(nil)
	d.lease.EXPECT().Complete(gomock.Any(), gomock.Any()).Return(errors.New("redis down"))
	d.repo.EXPECT().CountUnpublished(gomock.Any()).Return(0, nil)

	result, err := uc.Drain(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Published)
}

func TestDrain_LagCountFailureDoesNotFailThePass(t *testing.T) {
	uc, d := newTestUsecase(t)
	fin := &finisher{}

	d.repo.EXPECT().ClaimUnpublished(gomock.Any(), gomock.Any()).
		Return([]entity.Record{}, fin.fn(), nil)
	d.repo.EXPECT().CountUnpublished(gomock.Any()).Return(0, errors.New("db down"))

	result, err := uc.Drain(context.Background())
	require.NoError(t, err, "the lag metric is observability, not correctness")
	assert.Equal(t, 0, result.Remaining)
}
