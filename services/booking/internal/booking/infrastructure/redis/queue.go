package redis

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// Queue implements outbound.Queue: it reads two fields of the event's queue hash.
type Queue struct{ rdb goredis.UniversalClient }

var _ outbound.Queue = (*Queue)(nil)

// NewQueue wraps the client.
func NewQueue(rdb goredis.UniversalClient) *Queue { return &Queue{rdb: rdb} }

// Snapshot reads state and full_booked_at in one HMGET. A missing hash, or a time that is not an epoch, gives what could
// be read (the sold-out answer only labels what the stock already proved).
func (q *Queue) Snapshot(ctx context.Context, eventID string) (domain.QueueSnapshot, error) {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return domain.QueueSnapshot{}, err
	}
	vals, err := q.rdb.HMGet(ctx, ev.QueueHash(), "state", "full_booked_at").Result()
	if err != nil {
		return domain.QueueSnapshot{}, fmt.Errorf("read queue state: %w", err)
	}
	var s domain.QueueSnapshot
	if v, ok := vals[0].(string); ok {
		s.State = v
	}
	if v, ok := vals[1].(string); ok {
		epoch, err := strconv.ParseInt(v, 10, 64)
		if err != nil || epoch <= 0 {
			slog.Warn("booking: full_booked_at is not an epoch", slog.String("event", eventID))
		} else {
			s.FullBookedAt = time.Unix(epoch, 0).UTC()
		}
	}
	return s, nil
}
