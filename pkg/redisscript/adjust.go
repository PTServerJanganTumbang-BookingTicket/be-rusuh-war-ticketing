package redisscript

import (
	"context"
	_ "embed"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

//go:embed adjust_if_unchanged.lua
var adjustSrc string

var adjustScript = goredis.NewScript(adjustSrc)

// AdjustCode is the answer of adjust_if_unchanged.lua.
type AdjustCode string

// Answers of adjust_if_unchanged.lua (docs/lua-scripts.md §5).
const (
	AdjustAdjusted  AdjustCode = "ADJUSTED"
	AdjustChanged   AdjustCode = "CHANGED"
	AdjustMissing   AdjustCode = "MISSING"
	AdjustBelowZero AdjustCode = "BELOW_ZERO"
)

// AdjustResult is the script's answer. Value is the new count (ADJUSTED), the current count (CHANGED, BELOW_ZERO) or -1.
type AdjustResult struct {
	Code  AdjustCode
	Value int64
}

// AdjustIfUnchanged adds delta to stock:{ev}:<category> only if it still holds observed, and never below zero. A key that
// is gone is reported (AdjustMissing), never created. An error means the answer is unknown.
func AdjustIfUnchanged(ctx context.Context, rdb goredis.Scripter, ev rediskeys.Event, category string, observed, delta int64) (AdjustResult, error) {
	raw, err := adjustScript.Run(ctx, rdb, []string{ev.Stock(category)}, observed, delta).Result()
	if err != nil {
		return AdjustResult{}, fmt.Errorf("redisscript: adjust_if_unchanged: %w", err)
	}
	r, ok := raw.([]any)
	if !ok || len(r) != 2 {
		return AdjustResult{}, fmt.Errorf("redisscript: adjust_if_unchanged: unexpected reply %v", raw)
	}
	code, ok1 := r[0].(string)
	value, ok2 := r[1].(int64)
	if !ok1 || !ok2 {
		return AdjustResult{}, fmt.Errorf("redisscript: adjust_if_unchanged: unexpected reply %v", raw)
	}
	return AdjustResult{Code: AdjustCode(code), Value: value}, nil
}
