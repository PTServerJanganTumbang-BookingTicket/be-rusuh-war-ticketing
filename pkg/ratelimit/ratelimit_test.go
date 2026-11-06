package ratelimit

import (
	"context"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func TestAllow_rejectsInvalidLimit(t *testing.T) {
	l := New(goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"}))
	for _, lim := range []Limit{{}, {Capacity: 1, Refill: 1}, {Capacity: 1, Per: time.Second}, {Refill: 1, Per: time.Second}} {
		if _, err := l.Allow(context.Background(), "k", lim); err == nil {
			t.Errorf("Allow(%+v) = nil error, want invalid limit", lim)
		}
	}
}

// The limiter reports Redis failures; fail-open is the caller's choice.
func TestAllow_returnsRedisError(t *testing.T) {
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer func() { _ = rdb.Close() }()
	r, err := New(rdb).Allow(context.Background(), "k", Limit{Capacity: 1, Refill: 1, Per: time.Second})
	if err == nil {
		t.Fatal("Allow on unreachable Redis returned nil error")
	}
	if r.Allowed {
		t.Error("Allow on error reported Allowed")
	}
}
