// Package redis holds the Redis adapters for the payment-api service.
package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// Holds releases expired bookings' stock and NIK locks back to Redis.
//
// This mirrors admission-ctl's Holds adapter and uses the same pkg/redisscript
// Lua script, so the two release paths cannot drift. The webhook and the sweep
// both end a booking through the same function.
type Holds struct{ rdb goredis.Cmdable }

var _ portout.IHolds = (*Holds)(nil)

// NewHolds binds the release script to rdb.
func NewHolds(rdb goredis.Cmdable) *Holds { return &Holds{rdb: rdb} }

// Release adds the held seats back to stock:{ev}:<category> and deletes the
// booking's nik:used:{ev}:<hmac> keys. When seats are present, this is atomic
// via the Lua script. When only NIKs are present (no seats), the NIK keys are
// deleted directly. A stock key that is gone is not recreated; stockMissing
// counts them (stock reconciliation restores them).
//
// Call it once per expired booking. Calling it twice double-credits inventory
// and oversells the event.
func (h *Holds) Release(ctx context.Context, eventID string, seats []portout.SeatCount, niks []string) (stockMissing int, err error) {
	if len(seats) == 0 && len(niks) == 0 {
		// Nothing to release. This can happen when a booking had no items
		// (an incomplete hold) or when the caller is defensive.
		return 0, nil
	}

	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return 0, err
	}

	// F3: When there are NIKs but no seats, the Lua script cannot run (it
	// requires at least one category). Delete the NIK keys directly. This
	// handles the edge case where a hold transaction wrote NIK claims but
	// no booking_items, which should not happen but is worth handling.
	if len(seats) == 0 {
		// len(niks) > 0 guaranteed by the guard above
		keys := make([]string, len(niks))
		for i, hmac := range niks {
			keys[i] = ev.NIKUsed(hmac)
		}
		if err := h.rdb.Del(ctx, keys...).Err(); err != nil {
			return 0, err
		}
		return 0, nil
	}

	cats := make([]redisscript.CategoryQty, len(seats))
	for i, s := range seats {
		cats[i] = redisscript.CategoryQty{Category: s.CategoryID, Qty: s.Qty}
	}
	res, err := redisscript.Release(ctx, h.rdb, ev, cats, niks)
	if err != nil {
		return 0, err
	}
	return res.StockMissing, nil
}
