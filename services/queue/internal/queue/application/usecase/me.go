package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

// MeInput is one GET /queue/me request, already parsed by the HTTP layer.
type MeInput struct {
	EventID    string
	DeviceID   string // X-Device-Id
	Bearer     string // access token without "Bearer "
	QueueToken string // X-Queue-Token
}

// MeResult is where the customer is (api-contracts §3.3).
type MeResult struct {
	State            string
	QueueToken       string
	TicketNo         int64
	NowServing       int64
	GrantToken       string
	BookingID        string
	BookingExpiresAt time.Time
	PaymentExpiresAt time.Time
	PaymentRef       string
	ServerTime       time.Time
}

// Me is the GET /queue/me use case. It is a read: a waiting customer is answered from Redis alone, and only a customer
// who already has a grant reads their bookings row.
type Me struct {
	cfg Config
	d   Deps
}

// NewMe builds the use case.
func NewMe(cfg Config, d Deps) *Me {
	if d.Clock == nil {
		d.Clock = time.Now
	}
	return &Me{cfg: cfg, d: d}
}

func (m *Me) caller() caller {
	return caller{tokens: m.d.Tokens, metrics: m.d.Metrics, pepper: m.cfg.DevicePepper}
}

// Get answers where the caller is:
//
//	event → identity → which token → token hash (or tombstone) → ownership → waiting | booking row
func (m *Me) Get(ctx context.Context, in MeInput) (MeResult, error) {
	ev, err := rediskeys.NewEvent(in.EventID)
	if err != nil {
		return MeResult{}, apperror.ErrNotFound("Event tidak ditemukan").WithCode(domain.CodeNotFound)
	}
	c := m.caller()
	accountID, err := c.identity(in.Bearer, in.DeviceID)
	if err != nil {
		return MeResult{}, err
	}

	qt, err := m.which(ctx, ev, in, accountID)
	if err != nil {
		return MeResult{}, err
	}
	e, found, err := m.d.Store.Engagement(ctx, qt)
	if err != nil {
		return MeResult{}, m.unavailable(ctx, "token lookup", err)
	}
	if !found || e.EventID != ev.ID() {
		return m.gone(ctx, qt, found)
	}
	if err := c.owns(e, accountID, c.deviceHash(in.DeviceID)); err != nil {
		return MeResult{}, err
	}

	res := MeResult{QueueToken: qt, TicketNo: e.TicketNo, ServerTime: m.d.Clock()}
	if e.GrantToken == "" {
		return m.waiting(ctx, ev, e, res)
	}
	return m.granted(ctx, ev, e, res)
}

// which picks the queue token: a Bearer caller gets their own account's place (a guest token is ignored, never taken
// over, BR-07); everyone else presents their token.
func (m *Me) which(ctx context.Context, ev rediskeys.Event, in MeInput, accountID string) (string, error) {
	if accountID != "" {
		qt, found, err := m.d.Store.AccountToken(ctx, ev, accountID)
		if err != nil {
			return "", m.unavailable(ctx, "acct lookup", err)
		}
		if !found {
			return "", apperror.ErrUnauthorized("Tidak ada antrian untuk akun ini").WithCode(domain.CodeUnauthorized)
		}
		return qt, nil
	}
	if in.QueueToken == "" {
		return "", apperror.ErrUnauthorized("Token antrian tidak ada").WithCode(domain.CodeUnauthorized)
	}
	return in.QueueToken, nil
}

// gone answers for a token that has no place: a paid booking leaves a tombstone (CONFIRMED); anything
// else is EXPIRED. A token of another event is just missing, so it never reads the tombstone.
func (m *Me) gone(ctx context.Context, qt string, otherEvent bool) (MeResult, error) {
	res := MeResult{State: domain.MeExpired, ServerTime: m.d.Clock()}
	if otherEvent {
		return res, nil
	}
	bookingID, found, err := m.d.Store.Settled(ctx, qt)
	if err != nil {
		return MeResult{}, m.unavailable(ctx, "tombstone lookup", err)
	}
	if found {
		return MeResult{State: domain.MeConfirmed, BookingID: bookingID, ServerTime: m.d.Clock()}, nil
	}
	return res, nil
}

// waiting is a customer with no grant yet: still in line, or admitted and about to be finished by admission-ctl.
func (m *Me) waiting(ctx context.Context, ev rediskeys.Event, e domain.Engagement, res MeResult) (MeResult, error) {
	ns, err := m.d.Store.NowServing(ctx, ev)
	if err != nil {
		return MeResult{}, m.unavailable(ctx, "now_serving", err)
	}
	res.State, res.NowServing = domain.MeQueued, ns
	if e.TicketNo > ns {
		return res, nil // still behind: nothing else to read
	}
	inLine, err := m.d.Store.InLine(ctx, ev, e.QueueToken)
	if err != nil {
		return MeResult{}, m.unavailable(ctx, "by_ticket", err)
	}
	if inLine {
		return res, nil // admitted, grant not written yet: the client retries in a second
	}
	// Not in line either. The grant may have landed between our two reads (the copy happens before the ZREM): look once more.
	again, found, err := m.d.Store.Engagement(ctx, e.QueueToken)
	if err != nil {
		return MeResult{}, m.unavailable(ctx, "token re-read", err)
	}
	if found && again.GrantToken != "" {
		return m.granted(ctx, ev, again, res)
	}
	m.lost(ctx, ev, e.QueueToken, "admitted but in neither the line nor a grant")
	return res, nil
}

// granted reads the customer's booking row, which is the authority on the current status.
func (m *Me) granted(ctx context.Context, ev rediskeys.Event, e domain.Engagement, res MeResult) (MeResult, error) {
	if m.d.Details == nil { // no Aurora connection configured: say so instead of panicking
		return MeResult{}, m.unavailable(ctx, "booking lookup", fmt.Errorf("no database connection"))
	}
	d, found, err := m.d.Details.Detail(ctx, ev.ID(), e.QueueToken)
	if err != nil {
		return MeResult{}, m.unavailable(ctx, "booking lookup", err)
	}
	if !found {
		// The token says granted, and the row is written before the token is: this is not lag.
		m.lost(ctx, ev, e.QueueToken, "token holds a grant but no booking row exists")
		ns, err := m.d.Store.NowServing(ctx, ev)
		if err != nil {
			return MeResult{}, m.unavailable(ctx, "now_serving", err)
		}
		res.State, res.NowServing = domain.MeQueued, ns
		return res, nil
	}
	grant := d.GrantToken
	if grant == "" {
		grant = e.GrantToken
	}
	switch d.Status {
	case "GRANTED":
		res.State, res.GrantToken, res.BookingExpiresAt = domain.MeGranted, grant, d.BookingExpiresAt
	case "SELECTING":
		res.State, res.GrantToken, res.BookingID, res.BookingExpiresAt = domain.MeSelecting, grant, d.ID, d.BookingExpiresAt
	case "HELD": // the 5-minute deadline no longer counts once Pay was pressed: the 10-minute payment one does
		res.State, res.GrantToken, res.BookingID, res.PaymentExpiresAt = domain.MeHeld, grant, d.ID, d.PaymentExpiresAt
	case "AWAITING_PAYMENT", "PENDING_CANCELLATION":
		res.State, res.GrantToken, res.BookingID = domain.MeAwaitingPayment, grant, d.ID
		res.PaymentExpiresAt, res.PaymentRef = d.PaymentExpiresAt, d.PaymentRef
	case "CONFIRMED":
		res.State, res.BookingID = domain.MeConfirmed, d.ID
	case "EXPIRED", "LAPSED", "ABANDONED":
		res.State = domain.MeExpired
	default:
		return MeResult{}, m.unavailable(ctx, "booking status", fmt.Errorf("unknown status %q", d.Status))
	}
	return res, nil
}

func (m *Me) lost(ctx context.Context, ev rediskeys.Event, qt, why string) {
	m.d.Metrics.Incr(domain.MetricLostGrant)
	slog.ErrorContext(ctx, "me: lost grant", slog.String("event_id", ev.ID()), slog.String("queue_token", qt), slog.String("why", why))
}

func (m *Me) unavailable(ctx context.Context, what string, err error) error {
	slog.ErrorContext(ctx, "me: lookup failed", slog.String("what", what), slog.Any("err", err))
	return errUnavailable()
}
