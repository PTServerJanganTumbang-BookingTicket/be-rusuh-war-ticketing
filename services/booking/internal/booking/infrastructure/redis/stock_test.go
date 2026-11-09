package redis

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

var ctx = context.Background()

func TestLevelsReadsEveryCategoryAndReportsTheMissingKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, _ := rediskeys.NewEvent("evt_a")
	mr.Set(ev.Stock("cat_vip"), "12")
	mr.Set(ev.Stock("cat_trib"), "-1")

	levels, missing, err := NewStock(rdb).Levels(ctx, "evt_a", []string{"cat_vip", "cat_fest", "cat_trib"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(levels, map[string]int64{"cat_vip": 12, "cat_trib": -1}) || !reflect.DeepEqual(missing, []string{"cat_fest"}) {
		t.Fatalf("levels %v missing %v", levels, missing)
	}
}

func TestLevelsRejectsAnInvalidEventIDOrAGarbledCount(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	if _, _, err := NewStock(rdb).Levels(ctx, "bad{id}", []string{"cat_vip"}); err == nil {
		t.Fatal("an event id rediskeys refuses must be an error")
	}
	ev, _ := rediskeys.NewEvent("evt_a")
	mr.Set(ev.Stock("cat_vip"), "lots")
	if _, _, err := NewStock(rdb).Levels(ctx, "evt_a", []string{"cat_vip"}); err == nil {
		t.Fatal("a stock value that is not a number must be an error, not a guess")
	}
}

// All stock keys of one event carry the {ev} hash tag, so one MGET works on the cluster.
func TestLevelsWorksInOneCallOnTheRedisCluster(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" || testing.Short() {
		t.Skip("set REDIS_ADDR (make verify does)")
	}
	rdb := goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{addr}})
	t.Cleanup(func() { _ = rdb.Close() })
	id := fmt.Sprintf("evt_stock_%d", time.Now().UnixNano())
	ev, _ := rediskeys.NewEvent(id)
	cats := []string{"cat_vip", "cat_fest", "cat_trib"}
	for i, c := range cats {
		rdb.Set(ctx, ev.Stock(c), i*10, time.Minute)
	}
	t.Cleanup(func() {
		for _, c := range cats {
			rdb.Del(ctx, ev.Stock(c))
		}
	})
	levels, missing, err := NewStock(rdb).Levels(ctx, id, cats)
	if err != nil || len(missing) != 0 || !reflect.DeepEqual(levels, map[string]int64{"cat_vip": 0, "cat_fest": 10, "cat_trib": 20}) {
		t.Fatalf("levels %v missing %v err %v", levels, missing, err)
	}
}
