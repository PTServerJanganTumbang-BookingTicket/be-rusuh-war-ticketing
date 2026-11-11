package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

// Holds implements outbound.Holds on pkg/redisscript's release_hold.lua: the same script booking runs when its own
// transaction fails after a hold.
type Holds struct{ rdb goredis.Scripter }

var _ outbound.Holds = (*Holds)(nil)

// NewHolds binds the script to rdb.
func NewHolds(rdb goredis.Scripter) *Holds { return &Holds{rdb: rdb} }

// Release adds the held seats back to stock:{ev}:<category> and deletes the booking's nik:used keys, atomically. A stock
// key that is gone is not recreated; stockMissing counts them. Call it once per expired hold.
func (h *Holds) Release(ctx context.Context, e domain.EndedHold) (int, error) {
	ev, err := rediskeys.NewEvent(e.EventID)
	if err != nil {
		return 0, err
	}
	cats := make([]redisscript.CategoryQty, len(e.Seats))
	for i, s := range e.Seats {
		cats[i] = redisscript.CategoryQty{Category: s.CategoryID, Qty: s.Qty}
	}
	res, err := redisscript.Release(ctx, h.rdb, ev, cats, e.NIKs)
	if err != nil {
		return 0, err
	}
	return res.StockMissing, nil
}
