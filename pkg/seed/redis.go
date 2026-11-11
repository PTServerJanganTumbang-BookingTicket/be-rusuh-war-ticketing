package seed

import (
	"context"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

// Redis creates the sale state and stock of each event, only for keys and fields that do not exist yet.
// A running sale is never touched: a second run cannot reset issued (reissued numbers would collide with by_ticket),
// now_serving, state or stock. It returns how many fields/keys it created.
//
// now stamps full_booked_at for a FULL_BOOKED event. admission-ctl closes such a sale 930 s after it, so the stamp
// is "now", not a fixed date: the demo keeps its FULL_BOOKED state for a while after seeding.
func Redis(ctx context.Context, rdb goredis.Cmdable, events []Event, now time.Time) (int, error) {
	created := 0
	for _, e := range events {
		ev, err := rediskeys.NewEvent(e.ID)
		if err != nil {
			return created, err
		}
		fields := [][2]string{
			{rediskeys.FieldState, e.State},
			{rediskeys.FieldIssued, "0"},
			{rediskeys.FieldNowServing, "0"},
			{rediskeys.FieldName, e.Name},
		}
		if !e.SaleStarts.IsZero() {
			fields = append(fields, [2]string{rediskeys.FieldSaleStartsAt, e.SaleStarts.UTC().Format(time.RFC3339)})
		}
		if e.State == StateFullBooked {
			fields = append(fields, [2]string{rediskeys.FieldFullBookedAt, strconv.FormatInt(now.Unix(), 10)})
		}
		for _, f := range fields {
			ok, err := rdb.HSetNX(ctx, ev.QueueHash(), f[0], f[1]).Result()
			if err != nil {
				return created, fmt.Errorf("%s %s: %w", ev.QueueHash(), f[0], err)
			}
			if ok {
				created++
			}
		}
		for _, c := range e.Categories {
			ok, err := rdb.SetNX(ctx, ev.Stock(c.ID), c.Available, 0).Result()
			if err != nil {
				return created, fmt.Errorf("%s: %w", ev.Stock(c.ID), err)
			}
			if ok {
				created++
			}
		}
	}
	return created, nil
}

// ResetRedis deletes the Redis keys of exactly these events (queue hash, line, stock), so a seed can be re-run with a
// different state. Local use only: it throws away tickets already issued for them.
func ResetRedis(ctx context.Context, rdb goredis.Cmdable, events []Event) error {
	for _, e := range events {
		ev, err := rediskeys.NewEvent(e.ID)
		if err != nil {
			return err
		}
		keys := []string{ev.QueueHash(), ev.ByTicket()}
		for _, c := range e.Categories {
			keys = append(keys, ev.Stock(c.ID))
		}
		for _, k := range keys { // one key per call: in a cluster the keys of different events sit on different slots
			if err := rdb.Del(ctx, k).Err(); err != nil {
				return fmt.Errorf("delete %s: %w", k, err)
			}
		}
	}
	return nil
}
