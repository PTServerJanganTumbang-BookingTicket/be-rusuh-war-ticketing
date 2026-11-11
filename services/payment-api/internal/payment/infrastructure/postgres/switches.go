package postgres

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// latestSwitches reduces the append-only log to its current state.
//
// DISTINCT ON takes the first row of each (method_id, bank_code) group in the
// ORDER BY, which is the newest. The id tie-break is not decoration: two
// inserts in the same microsecond would otherwise order non-deterministically,
// and whichever the planner happened to return would decide whether a bank is
// on.
//
// Expired switches are filtered here rather than in Go so a stale cache cannot
// keep one alive past its expiry: whatever this returns is already current.
// Only rows that turn something OFF come back — an `available = true` row
// exists to end a switch and record who ended it, and carries no meaning of
// its own once it is the latest.
const latestSwitches = `
  SELECT method_id, COALESCE(bank_code, ''), available
  FROM (
    SELECT DISTINCT ON (method_id, bank_code) method_id, bank_code, available
    FROM payment_method_switches
    WHERE expires_at IS NULL OR expires_at > now()
    ORDER BY method_id, bank_code, changed_at DESC, id DESC
  ) latest
  WHERE available = false`

// SwitchRepository reads the operator kill switch, with a short cache.
//
// Cached because §5.1 is hit by every customer on the payment page during a
// sale, and this is one more query on that path for state that changes a
// handful of times a year. The TTL is the honest cost: a switch takes effect
// within it rather than instantly. Seconds is the right unit — the lever
// exists to replace a fifteen-minute deploy, not to be synchronous.
type SwitchRepository struct {
	db  DBConn
	ttl time.Duration
	now func() time.Time

	mu     sync.Mutex
	cached entity.Disabled
	// readAt is when cached was last refreshed from the database. The zero
	// value means never, which is distinguishable from "read and empty" —
	// and the difference matters, because an empty set is the normal state.
	readAt time.Time
}

// NewSwitchRepository wires the repository. A ttl of zero or less disables
// caching, which is what the tests want and what a debugging session wants.
func NewSwitchRepository(db DBConn, ttl time.Duration, now func() time.Time) portout.IMethodSwitches {
	if now == nil {
		now = time.Now
	}
	return &SwitchRepository{db: db, ttl: ttl, now: now}
}

// Disabled returns the methods and banks currently switched off.
func (r *SwitchRepository) Disabled(ctx context.Context) (entity.Disabled, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if !r.readAt.IsZero() && r.ttl > 0 && now.Sub(r.readAt) < r.ttl {
		return r.cached, nil
	}

	rows, err := r.db.Query(ctx, latestSwitches)
	if err != nil {
		return r.fallback(err)
	}
	defer rows.Close()

	out := entity.Disabled{}
	for rows.Next() {
		var methodID, bankCode string
		var available bool
		if err := rows.Scan(&methodID, &bankCode, &available); err != nil {
			return r.fallback(err)
		}
		out[entity.MethodSwitch{MethodID: methodID, BankCode: bankCode}] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return r.fallback(err)
	}

	r.cached, r.readAt = out, now
	return out, nil
}

// fallback answers a failed read with the last known state, and with "nothing
// is switched off" when there has never been one.
//
// Never with an error the caller must handle: a kill switch whose own storage
// is unreachable must not be able to take payments down. The worst case of
// guessing "available" here is that a switched-off method is offered during a
// database outage — in which case §5.2 cannot charge anything either, because
// it needs the same database to lock the booking.
func (r *SwitchRepository) fallback(err error) (entity.Disabled, error) {
	if r.readAt.IsZero() {
		slog.Warn("payment method switches unreadable, treating nothing as switched off",
			slog.Any("err", err))
		return entity.Disabled{}, nil
	}
	slog.Warn("payment method switches unreadable, reusing the last known state",
		slog.Any("err", err), slog.Int("switches", len(r.cached)),
		slog.Time("read_at", r.readAt))
	return r.cached, nil
}
