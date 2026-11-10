package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"

	mock_inbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/_mock/outbox/port/inbound"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/config"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher/internal/outbox/port/inbound"
)

func newRunner(uc portin.IPublisherUsecase) *Runner {
	return &Runner{
		Usecase: uc,
		cfg: config.Config{
			App: config.App{
				Interval:    time.Minute,
				DrainBudget: 5 * time.Second,
			},
		},
	}
}

// ── Close ─────────────────────────────────────────────────────────────────────

func TestRunner_Close_NilFields(t *testing.T) {
	r := &Runner{}
	assert.NotPanics(t, r.Close) // must not panic with nil pool / redis
}

// ── Interval / DrainBudget ────────────────────────────────────────────────────

func TestRunner_Interval(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIPublisherUsecase(ctrl)
	r := newRunner(uc)
	assert.Equal(t, time.Minute, r.Interval())
}

func TestRunner_DrainBudget(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIPublisherUsecase(ctrl)
	r := newRunner(uc)
	assert.Equal(t, 5*time.Second, r.DrainBudget())
}

// ── DrainUntil ────────────────────────────────────────────────────────────────

func TestDrainUntil_EmptyBacklog(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIPublisherUsecase(ctrl)
	// Drain returns nothing published → loop exits
	uc.EXPECT().Drain(gomock.Any()).Return(portin.Result{Published: 0, Remaining: 0}, nil)

	r := newRunner(uc)
	total, err := r.DrainUntil(context.Background(), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 0, total.Published)
}

func TestDrainUntil_DrainError(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIPublisherUsecase(ctrl)
	uc.EXPECT().Drain(gomock.Any()).Return(portin.Result{}, errors.New("drain failed"))

	r := newRunner(uc)
	_, err := r.DrainUntil(context.Background(), time.Minute)
	assert.Error(t, err)
}

func TestDrainUntil_StopsWhenBudgetExhausted(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIPublisherUsecase(ctrl)
	// Every Drain call publishes something so the loop only stops when budget runs out
	uc.EXPECT().Drain(gomock.Any()).Return(
		portin.Result{Claimed: 5, Published: 5, Remaining: 100}, nil,
	).AnyTimes()

	r := newRunner(uc)
	// Very short budget so the loop exits immediately after one pass
	total, err := r.DrainUntil(context.Background(), 1*time.Nanosecond)
	require.NoError(t, err)
	assert.Positive(t, total.Published)
}

func TestDrainUntil_AccumulatesResults(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIPublisherUsecase(ctrl)
	gomock.InOrder(
		uc.EXPECT().Drain(gomock.Any()).Return(portin.Result{Claimed: 3, Published: 3, Remaining: 2}, nil),
		uc.EXPECT().Drain(gomock.Any()).Return(portin.Result{Claimed: 2, Published: 2, Remaining: 0}, nil),
		uc.EXPECT().Drain(gomock.Any()).Return(portin.Result{Published: 0}, nil),
	)

	r := newRunner(uc)
	total, err := r.DrainUntil(context.Background(), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 5, total.Published)
	assert.Equal(t, 5, total.Claimed)
}

// ── New() error path (no real resources needed) ───────────────────────────────

func TestNew_FailsWithInvalidDBDSN(t *testing.T) {
	_, err := New(context.Background(), config.Config{
		DB: config.DB{DSN: "badscheme://user:pass@localhost/db"},
	})
	assert.Error(t, err) // fails at NewPool (ParseConfig error)
}
