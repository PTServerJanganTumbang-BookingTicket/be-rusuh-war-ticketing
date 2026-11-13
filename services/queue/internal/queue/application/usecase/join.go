// Package usecase holds the queue's application logic.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/port/outbound"
)

// Config is the join policy.
type Config struct {
	Capacity     int64           // QUEUE_CAPACITY
	SaleWindow   time.Duration   // SALE_WINDOW_SECONDS: TTL of token and acct keys
	ClaimWait    time.Duration   // rejoin: how long an acct pointer without a token may be in flight before it is stale
	RaceWait     time.Duration   // lost acct claim: how long to wait for the winning join's token
	RateLimits   bool            // off only in load tests
	IPLimit      ratelimit.Limit // rl:join:<ip>
	AccountLimit ratelimit.Limit // rl:joinacct:<id>
	DevicePepper []byte          // DEVICE_PEPPER for device_hash
}

// DefaultConfig holds the contract's values.
func DefaultConfig() Config {
	return Config{
		Capacity:     300000,
		SaleWindow:   21600 * time.Second,
		ClaimWait:    300 * time.Millisecond,
		RaceWait:     2 * time.Second,
		RateLimits:   true,
		IPLimit:      ratelimit.Limit{Capacity: 20, Refill: 20, Per: 5 * time.Minute},
		AccountLimit: ratelimit.Limit{Capacity: 3, Refill: 3, Per: time.Minute},
	}
}

// Deps are the join use case's collaborators.
type Deps struct {
	Store    outbound.QueueStore
	Bookings outbound.BookingReader
	Details  outbound.BookingDetails // /queue/me only
	Captcha  outbound.Captcha
	Limiter  outbound.RateLimiter
	Tokens   outbound.TokenVerifier
	Metrics  outbound.Metrics
	Clock    func() time.Time
	NewToken func() string // queue_token generator ("qt_" + ULID)
}

// JoinInput is one join request, already parsed by the HTTP layer.
type JoinInput struct {
	EventID      string
	CaptchaToken string
	DeviceID     string // X-Device-Id
	Bearer       string // access token without "Bearer "
	QueueToken   string // X-Queue-Token (BR-09)
	ClientIP     string // from the gateway, never from X-Forwarded-For
}

// Outcome says which of the two 200 responses to send.
type Outcome int

// Join outcomes.
const (
	Queued Outcome = iota
	Rejoined
)

// JoinResult is a successful join.
type JoinResult struct {
	Outcome    Outcome
	Engagement domain.Engagement
	NowServing int64
	EventName  string
	State      string // Rejoined only
}

// Position is ticket_no − now_serving.
func (r JoinResult) Position() int64 { return r.Engagement.TicketNo - r.NowServing }

// Join is the join use case.
type Join struct {
	cfg Config
	d   Deps
}

// NewJoin builds the use case.
func NewJoin(cfg Config, d Deps) *Join {
	if d.Clock == nil {
		d.Clock = time.Now
	}
	return &Join{cfg: cfg, d: d}
}

// ErrForeignToken marks a rejection because the presented queue_token belongs
// to another device or account.
var ErrForeignToken = errors.New("queue token belongs to another device or account")

// errForeignToken is a 401 that matches both *apperror.AppError and ErrForeignToken.
func errForeignToken(code, message string) error {
	return fmt.Errorf("%w: %w", ErrForeignToken, apperror.New(http.StatusUnauthorized, "40100", message).WithCode(code))
}

func errUnavailable() *apperror.AppError {
	return apperror.New(http.StatusServiceUnavailable, "50300", "Antrian sedang tidak tersedia, silakan coba lagi")
}

// Join runs the gates in order, then issues a place:
//
//	shape → identity → rate limit → CAPTCHA → sale state → rejoin → sold out → capacity → issue
//
// Every rejection happens before INCR, so it never burns a ticket number.
func (j *Join) Join(ctx context.Context, in JoinInput) (JoinResult, error) {
	ev, err := rediskeys.NewEvent(in.EventID)
	if err != nil {
		return JoinResult{}, apperror.ErrNotFound("Event tidak ditemukan").WithCode(domain.CodeNotFound)
	}

	accountID, err := j.identity(in)
	if err != nil {
		return JoinResult{}, err
	}

	if err := j.rateLimit(ctx, in.ClientIP, accountID); err != nil {
		return JoinResult{}, err
	}

	if err := j.captcha(ctx, in); err != nil {
		return JoinResult{}, err
	}

	snap, err := j.d.Store.Snapshot(ctx, ev)
	if err != nil {
		slog.ErrorContext(ctx, "join: snapshot failed", slog.String("event_id", ev.ID()), slog.Any("err", err))
		return JoinResult{}, errUnavailable()
	}
	if err := saleGate(snap); err != nil {
		return JoinResult{}, err
	}

	deviceHash := j.deviceHash(in.DeviceID)
	res, ok, stale, err := j.rejoin(ctx, ev, snap, in.QueueToken, accountID, deviceHash)
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return JoinResult{}, err // a deliberate rejection (e.g. foreign token), not an outage
	}
	if err != nil {
		slog.ErrorContext(ctx, "join: rejoin lookup failed", slog.String("event_id", ev.ID()), slog.Any("err", err))
		return JoinResult{}, errUnavailable()
	}
	if ok {
		return res, nil
	}

	if snap.Depth() >= j.cfg.Capacity {
		return JoinResult{}, apperror.New(http.StatusServiceUnavailable, "50300",
			"Antrian sedang penuh, silakan coba beberapa saat lagi").WithCode(domain.CodeQueueFull).
			WithData(map[string]any{"queue_state": "FULL_QUEUE", "queue_capacity": j.cfg.Capacity})
	}

	return j.issue(ctx, ev, snap, accountID, deviceHash, stale)
}

func (j *Join) caller() caller {
	return caller{tokens: j.d.Tokens, metrics: j.d.Metrics, pepper: j.cfg.DevicePepper}
}

func (j *Join) identity(in JoinInput) (accountID string, err error) {
	return j.caller().identity(in.Bearer, in.DeviceID)
}

func (j *Join) rateLimit(ctx context.Context, ip, accountID string) error {
	if !j.cfg.RateLimits {
		return nil
	}
	if err := j.allow(ctx, rediskeys.JoinRateLimit(ip), j.cfg.IPLimit); err != nil {
		return err
	}
	if accountID != "" {
		return j.allow(ctx, rediskeys.JoinAcctLimit(accountID), j.cfg.AccountLimit)
	}
	return nil
}

// allow fails open on limiter errors: fairness, not correctness, is at stake.
func (j *Join) allow(ctx context.Context, key string, lim ratelimit.Limit) error {
	r, err := j.d.Limiter.Allow(ctx, key, lim)
	if err != nil {
		j.d.Metrics.Incr(domain.MetricRateLimitFailOpen)
		slog.WarnContext(ctx, "join: rate limiter unavailable, failing open", slog.Any("err", err))
		return nil
	}
	if r.Allowed {
		return nil
	}
	seconds := max(int(math.Ceil(r.RetryAfter.Seconds())), 1)
	return apperror.ErrTooManyRequests("Terlalu banyak percobaan").WithCode(domain.CodeTooManyRequests).
		WithRetryAfter(time.Duration(seconds) * time.Second).
		WithData(map[string]any{"retry_after": seconds})
}

func (j *Join) captcha(ctx context.Context, in JoinInput) error {
	if in.CaptchaToken == "" {
		return apperror.ErrBadRequest("Verifikasi CAPTCHA diperlukan").WithCode(domain.CodeCaptchaRequired)
	}
	start := time.Now()
	switch j.d.Captcha.Verify(ctx, in.CaptchaToken, in.ClientIP) {
	case domain.CaptchaValid:
		return nil
	case domain.CaptchaInvalid:
		return apperror.ErrForbidden("Verifikasi CAPTCHA gagal, ambil token baru").WithCode(domain.CodeCaptchaFailed)
	default:
		j.d.Metrics.Record(domain.MetricCaptchaFailOpen, time.Since(start).Seconds())
		slog.WarnContext(ctx, "join: CAPTCHA provider unavailable, failing open")
		return nil
	}
}

func saleGate(s domain.EventSnapshot) error {
	switch {
	case !s.State.Known():
		return apperror.New(http.StatusServiceUnavailable, "50300", "Antrian belum siap").WithCode(domain.CodeQueueNotReady)
	case s.State == domain.StateScheduled:
		return apperror.ErrConflict("Penjualan belum dibuka").WithCode(domain.CodeSaleNotOpen).
			WithData(map[string]any{"queue_state": "SCHEDULED", "sale_starts_at": s.SaleStartsAt})
	case s.State == domain.StateClosed:
		return apperror.New(http.StatusGone, "41000", "Tiket sudah habis").WithCode(domain.CodeSaleClosed).
			WithData(map[string]any{"queue_state": string(domain.StateClosed)})
	}
	return nil
}

// rejoin returns the existing place (BR-09 token, then BR-07 account). stale
// is an acct pointer whose token no longer exists, to be replaced on issue.
//
// A presented token of this event rejoins only for its owner (see owns).
// Unknown tokens and tokens of another event are ignored.
func (j *Join) rejoin(ctx context.Context, ev rediskeys.Event, snap domain.EventSnapshot, presented, accountID, deviceHash string) (res JoinResult, ok bool, stale string, err error) {
	if presented != "" {
		e, found, err := j.d.Store.Engagement(ctx, presented)
		if err != nil {
			return JoinResult{}, false, "", err
		}
		if found && e.EventID == ev.ID() {
			if err := j.owns(e, accountID, deviceHash); err != nil {
				return JoinResult{}, false, "", err
			}
			if e.AccountID != "" || accountID == "" {
				res, err := j.restore(ctx, ev, snap, e, false)
				return res, err == nil, "", err
			}
		}
	}
	if accountID == "" {
		return JoinResult{}, false, "", nil
	}
	qt, found, err := j.d.Store.AccountToken(ctx, ev, accountID)
	if err != nil || !found {
		return JoinResult{}, false, "", err
	}
	e, found, err := j.waitEngagement(ctx, qt, j.cfg.ClaimWait)
	if err != nil {
		return JoinResult{}, false, "", err
	}
	if !found || e.EventID != ev.ID() {
		return JoinResult{}, false, qt, nil
	}
	res, err = j.restore(ctx, ev, snap, e, true)
	return res, err == nil, "", err
}

func (j *Join) owns(e domain.Engagement, accountID, deviceHash string) error {
	return j.caller().owns(e, accountID, deviceHash)
}

// waitEngagement reads token:<qt>, waiting up to `wait` for a concurrent
// join that has claimed acct but not yet written its token.
func (j *Join) waitEngagement(ctx context.Context, qt string, wait time.Duration) (domain.Engagement, bool, error) {
	deadline := time.Now().Add(wait)
	for {
		e, found, err := j.d.Store.Engagement(ctx, qt)
		if err != nil || found || !time.Now().Before(deadline) {
			return e, found, err
		}
		select {
		case <-ctx.Done():
			return domain.Engagement{}, false, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// restore builds the "rejoined" answer.
//
// Still waiting (ticket_no > now_serving): a place missing from by_ticket (a
// join that died between HSET and ZADD) is put back with its own number, but
// only while it is still ahead of now_serving, atomically with admission-ctl's
// advance. A served ticket is never re-added: admission-ctl would grant it again.
//
// Turn has come: the booking row says where the customer is. No row yet means
// a pending grant that admission-ctl finishes from by_ticket (QUEUED). A failed
// lookup is an error, not a guess.
func (j *Join) restore(ctx context.Context, ev rediskeys.Event, snap domain.EventSnapshot, e domain.Engagement, viaAccount bool) (JoinResult, error) {
	state := domain.EngagementQueued
	if e.TicketNo <= snap.NowServing {
		if j.d.Bookings != nil {
			b, found, err := j.d.Bookings.ByQueueToken(ctx, ev.ID(), e.QueueToken)
			if err != nil {
				return JoinResult{}, fmt.Errorf("booking lookup on rejoin: %w", err)
			}
			if found {
				state = b.Status
			}
		}
	} else {
		added, err := j.d.Store.RepairLine(ctx, ev, e.QueueToken, e.TicketNo)
		if err != nil {
			return JoinResult{}, err
		}
		if added {
			j.d.Metrics.Incr(domain.MetricJoinRepaired)
		}
	}
	if viaAccount {
		j.d.Metrics.Incr(domain.MetricBR07Recovered)
	}
	return JoinResult{Outcome: Rejoined, Engagement: e, NowServing: snap.NowServing, EventName: snap.Name, State: state}, nil
}

func (j *Join) issue(ctx context.Context, ev rediskeys.Event, snap domain.EventSnapshot, accountID, deviceHash, stale string) (JoinResult, error) {
	qt := j.d.NewToken()
	if accountID != "" {
		claimed, current, err := j.d.Store.ClaimAccount(ctx, ev, accountID, qt, stale, j.cfg.SaleWindow)
		if err != nil {
			slog.ErrorContext(ctx, "join: account claim failed", slog.String("event_id", ev.ID()), slog.Any("err", err))
			return JoinResult{}, errUnavailable()
		}
		if !claimed {
			// A concurrent join of the same account won: return its place.
			e, found, err := j.waitEngagement(ctx, current, j.cfg.RaceWait)
			if err != nil || !found {
				return JoinResult{}, errUnavailable()
			}
			res, err := j.restore(ctx, ev, snap, e, true)
			if err != nil {
				slog.ErrorContext(ctx, "join: restoring the winning place failed", slog.String("event_id", ev.ID()), slog.Any("err", err))
				return JoinResult{}, errUnavailable()
			}
			return res, nil
		}
	}
	release := func() {
		if accountID != "" {
			_ = j.d.Store.ReleaseAccount(context.WithoutCancel(ctx), ev, accountID, qt)
		}
	}

	ticketNo, err := j.d.Store.IssueTicket(ctx, ev)
	if err != nil {
		release()
		slog.ErrorContext(ctx, "join: INCR issued failed", slog.String("event_id", ev.ID()), slog.Any("err", err))
		return JoinResult{}, errUnavailable()
	}
	e := domain.Engagement{
		QueueToken: qt,
		EventID:    ev.ID(),
		TicketNo:   ticketNo,
		AccountID:  accountID,
		DeviceHash: deviceHash,
		JoinedAt:   j.d.Clock().UTC(),
	}
	// Token before by_ticket: admission-ctl must never see a member without its
	// hash. A failure in between leaves a harmless gap or a place that a rejoin repairs.
	if err := j.d.Store.SaveEngagement(ctx, e, j.cfg.SaleWindow); err != nil {
		release()
		slog.ErrorContext(ctx, "join: saving token failed", slog.String("event_id", ev.ID()), slog.Any("err", err))
		return JoinResult{}, errUnavailable()
	}
	if err := j.d.Store.AddToLine(ctx, ev, qt, ticketNo); err != nil {
		release()
		slog.ErrorContext(ctx, "join: ZADD by_ticket failed", slog.String("event_id", ev.ID()), slog.Any("err", err))
		return JoinResult{}, errUnavailable()
	}

	res := JoinResult{Outcome: Queued, Engagement: e, NowServing: snap.NowServing, EventName: snap.Name}
	return res, nil
}

func (j *Join) deviceHash(deviceID string) string { return j.caller().deviceHash(deviceID) }
