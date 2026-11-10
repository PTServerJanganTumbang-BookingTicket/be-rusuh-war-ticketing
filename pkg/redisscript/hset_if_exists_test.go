package redisscript

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestHSetIfExistsWritesOnlyToAnExistingHash(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	ctx := context.Background()

	if ok, err := HSetIfExists(ctx, rdb, "token:gone", "grant_token", "G1"); err != nil || ok || mr.Exists("token:gone") {
		t.Fatalf("a missing hash was written: ok=%v err=%v", ok, err)
	}
	mr.HSet("token:live", "ticket_no", "1")
	mr.SetTTL("token:live", time.Hour)
	if ok, err := HSetIfExists(ctx, rdb, "token:live", "grant_token", "G1", "booking_id", "B1"); err != nil || !ok {
		t.Fatalf("existing hash not written: ok=%v err=%v", ok, err)
	}
	if mr.HGet("token:live", "grant_token") != "G1" || mr.HGet("token:live", "booking_id") != "B1" || mr.TTL("token:live") != time.Hour {
		t.Fatal("fields or TTL wrong after the write")
	}
}

func TestHSetIfExistsNeedsFieldValuePairs(t *testing.T) {
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	defer func() { _ = rdb.Close() }()
	for _, args := range [][]any{nil, {"only-a-field"}, {"a", "b", "c"}} {
		if _, err := HSetIfExists(context.Background(), rdb, "k", args...); err == nil {
			t.Errorf("arguments %v were accepted", args)
		}
	}
}
