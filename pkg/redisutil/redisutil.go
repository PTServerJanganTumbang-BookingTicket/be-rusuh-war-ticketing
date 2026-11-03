// Package redisutil provides a shared Redis connection helper.
package redisutil

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// NewClient opens a Redis connection and verifies it answers.
//
// Pinging at startup is deliberate: a Lambda that starts healthy against an
// unreachable Redis would serve traffic with its guards silently degraded.
func NewClient(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{Addr: addr, Password: password, DB: db})
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}
