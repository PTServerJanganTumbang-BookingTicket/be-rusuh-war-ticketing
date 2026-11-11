package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// Holds implements outbound.Holds on pkg/redisscript's hold_booking.lua and release_hold.lua.
type Holds struct {
	rdb      goredis.Scripter
	maxTotal int64
	nikTTL   time.Duration
}

var _ outbound.Holds = (*Holds)(nil)

// NewHolds binds the scripts to rdb. maxTotal is MAX_TICKETS_PER_TRANSACTION; nikTTL is how long a NIK lock may leak
// (SALE_WINDOW_SECONDS: a lock whose booking never completed blocks that NIK for at most the sale).
func NewHolds(rdb goredis.Scripter, maxTotal int64, nikTTL time.Duration) *Holds {
	return &Holds{rdb: rdb, maxTotal: maxTotal, nikTTL: nikTTL}
}

// Hold takes the seats and locks the NIKs, all or nothing. An error means the answer is unknown.
func (h *Holds) Hold(ctx context.Context, eventID string, o domain.HoldOrder) (domain.HoldAnswer, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return domain.HoldAnswer{}, err
	}
	res, err := redisscript.Hold(ctx, h.rdb, redisscript.HoldRequest{
		Event: ev, Categories: categories(o), NIKs: o.NIKs, MaxTotal: h.maxTotal, NIKTTL: h.nikTTL,
	})
	if err != nil {
		return domain.HoldAnswer{}, err
	}
	return domain.HoldAnswer{Code: string(res.Code), Index: res.Index}, nil
}

// Release gives a hold back, once. A stock key that is gone is not recreated; it is counted in stockMissing.
func (h *Holds) Release(ctx context.Context, eventID string, o domain.HoldOrder) (int, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return 0, err
	}
	res, err := redisscript.Release(ctx, h.rdb, ev, categories(o), o.NIKs)
	if err != nil {
		return 0, err
	}
	return res.StockMissing, nil
}

func categories(o domain.HoldOrder) []redisscript.CategoryQty {
	out := make([]redisscript.CategoryQty, len(o.Categories))
	for i, c := range o.Categories {
		out[i] = redisscript.CategoryQty{Category: c.CategoryID, Qty: c.Qty}
	}
	return out
}
