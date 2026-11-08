package usecase_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	queuepg "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/postgres"
	queueredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/redis"
)

type noTokens struct{}

func (noTokens) Verify(string) (token.Claims, error) {
	return token.Claims{}, fmt.Errorf("no bearer here")
}

// hash is the device_hash join would store for a device id, with the test's pepper.
func hash(deviceID string) string {
	m := hmac.New(sha256.New, []byte("pepper"))
	m.Write([]byte(deviceID))
	return hex.EncodeToString(m.Sum(nil))
}

type quiet struct{}

func (quiet) Incr(string)            {}
func (quiet) Record(string, float64) {}

// /queue/me against the real Redis and a real Postgres: the token hash admission-ctl writes, then the booking row.
func TestMeOnRealRedisAndPostgres(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" || testing.Short() {
		t.Skip("set REDIS_ADDR and DATABASE_URL (make verify does)")
	}
	pool := pgtest.New(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()

	id := fmt.Sprintf("evt_me_%d", time.Now().UnixNano())
	ev, _ := rediskeys.NewEvent(id)
	qt := "qt_me_" + id
	t.Cleanup(func() { rdb.Del(ctx, ev.QueueHash(), ev.ByTicket(), rediskeys.Token(qt)) })
	rdb.HSet(ctx, ev.QueueHash(), "state", "OPEN", "now_serving", 10, "issued", 20)
	rdb.HSet(ctx, rediskeys.Token(qt), "ticket_no", 15, "event_id", id, "device_hash", hash("dev-1"))
	rdb.Expire(ctx, rediskeys.Token(qt), time.Hour)

	cfg := usecase.DefaultConfig()
	cfg.DevicePepper = []byte("pepper")
	pg := queuepg.NewBookings(pool)
	me := usecase.NewMe(cfg, usecase.Deps{Store: queueredis.NewStore(rdb), Details: pg, Metrics: quiet{}, Tokens: noTokens{}})
	in := usecase.MeInput{EventID: id, DeviceID: "dev-1", QueueToken: qt}

	// 1. behind now_serving: waiting, answered without a bookings row.
	rdb.HSet(ctx, ev.QueueHash(), "now_serving", 5)
	if r, err := me.Get(ctx, in); err != nil || r.State != domain.MeQueued || r.TicketNo != 15 || r.NowServing != 5 {
		t.Fatalf("waiting: %+v %v", r, err)
	}

	// 2. admission-ctl grants: row first, then the grant fields in the token hash.
	rdb.HSet(ctx, ev.QueueHash(), "now_serving", 20)
	if _, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, granted_at, booking_expires_at)
		VALUES ('RB-1', $1, $2, 'GRANTED', 'gt_1', now(), now() + interval '5 minutes')`, id, qt); err != nil {
		t.Fatal(err)
	}
	rdb.HSet(ctx, rediskeys.Token(qt), "grant_token", "gt_1", "booking_id", "RB-1")
	r, err := me.Get(ctx, in)
	if err != nil || r.State != domain.MeGranted || r.GrantToken != "gt_1" || r.BookingExpiresAt.IsZero() {
		t.Fatalf("granted: %+v %v", r, err)
	}

	// 3. the customer lapses: EXPIRED, and never the dead grant token.
	if _, err := pool.Exec(ctx, `UPDATE bookings SET status = 'LAPSED' WHERE id = 'RB-1'`); err != nil {
		t.Fatal(err)
	}
	if r, err := me.Get(ctx, in); err != nil || r.State != domain.MeExpired || r.GrantToken != "" {
		t.Fatalf("lapsed: %+v %v", r, err)
	}
}
