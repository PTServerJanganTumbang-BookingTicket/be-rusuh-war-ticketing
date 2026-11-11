package redisscript

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

//go:embed hold_booking.lua
var holdSrc string

//go:embed release_hold.lua
var releaseSrc string

var (
	holdScript    = goredis.NewScript(holdSrc)
	releaseScript = goredis.NewScript(releaseSrc)
)

// HoldCode is the answer of hold_booking.lua.
type HoldCode string

// Answers of hold_booking.lua (docs/lua-scripts.md §4).
const (
	HoldHeld               HoldCode = "HELD"
	HoldNoStock            HoldCode = "NO_STOCK"
	HoldStockUninitialized HoldCode = "STOCK_UNINITIALIZED"
	HoldNIKTaken           HoldCode = "NIK_TAKEN"
	HoldStockNegative      HoldCode = "STOCK_NEGATIVE"
	HoldQtyInvalid         HoldCode = "QTY_INVALID"
	HoldQtyExceeded        HoldCode = "QTY_EXCEEDED"
	HoldNIKCountMismatch   HoldCode = "NIK_COUNT_MISMATCH"
)

// CategoryQty is the number of seats wanted in one category. A category appears once per call.
type CategoryQty struct {
	Category string
	Qty      int64
}

// HoldRequest is one order: the seats per category and one NIK hash per seat.
type HoldRequest struct {
	Event      rediskeys.Event
	Categories []CategoryQty
	NIKs       []string      // nik_hmac of every seat; the same count as the seats
	MaxTotal   int64         // MAX_TICKETS_PER_TRANSACTION
	NIKTTL     time.Duration // how long a NIK lock may leak (SALE_WINDOW_SECONDS); whole seconds, at least one
}

// HoldResult is what hold_booking.lua answered. Index is the category (NO_STOCK, STOCK_UNINITIALIZED, STOCK_NEGATIVE,
// QTY_INVALID) or the seat (NIK_TAKEN), counted from 0 in the order of the request; -1 when it does not apply.
type HoldResult struct {
	Code  HoldCode
	Index int
}

// Hold runs hold_booking.lua: all the seats and NIK locks of the order, or none. A refusal changes nothing. An error
// means the answer is unknown (Redis unreachable, a malformed reply); the caller must not treat it as HELD. A code this
// package does not list is passed through as it is: callers handle HELD and each refusal explicitly and treat every
// other code as an internal error.
func Hold(ctx context.Context, rdb goredis.Scripter, req HoldRequest) (HoldResult, error) {
	if len(req.Categories) == 0 {
		return HoldResult{}, errors.New("redisscript: Hold needs at least one category")
	}
	if req.MaxTotal < 1 {
		// The zero value would answer QTY_EXCEEDED to every order.
		return HoldResult{}, errors.New("redisscript: Hold needs MaxTotal (MAX_TICKETS_PER_TRANSACTION) of at least 1")
	}
	ttl := int64(req.NIKTTL / time.Second)
	if len(req.NIKs) > 0 && ttl < 1 {
		// A lock without an expiry would live for ever if the booking never completes.
		return HoldResult{}, errors.New("redisscript: Hold needs a NIK lock TTL of at least one second")
	}
	keys := make([]string, 0, len(req.Categories)+len(req.NIKs))
	argv := []any{len(req.Categories), len(req.NIKs), req.MaxTotal, ttl}
	for _, c := range req.Categories {
		keys = append(keys, req.Event.Stock(c.Category))
		argv = append(argv, c.Qty)
	}
	for _, h := range req.NIKs {
		keys = append(keys, req.Event.NIKUsed(h))
	}
	raw, err := holdScript.Run(ctx, rdb, keys, argv...).Result()
	if err != nil {
		return HoldResult{}, fmt.Errorf("redisscript: hold_booking: %w", err)
	}
	r, ok := raw.([]any)
	if !ok || len(r) != 2 {
		return HoldResult{}, fmt.Errorf("redisscript: hold_booking: unexpected reply %v", raw)
	}
	code, ok1 := r[0].(string)
	idx, ok2 := r[1].(int64)
	if !ok1 || !ok2 {
		return HoldResult{}, fmt.Errorf("redisscript: hold_booking: unexpected reply %v", raw)
	}
	return HoldResult{Code: HoldCode(code), Index: int(idx)}, nil
}

// ReleaseResult is what release_hold.lua reports.
type ReleaseResult struct {
	NIKsDeleted  int // NIK locks that existed and were deleted
	StockMissing int // stock keys that were gone: not recreated, left for stock reconciliation (B5); count it
}

// Release runs release_hold.lua: it adds the seats of a hold back to the stock and deletes the NIK locks, in one call.
// Call it once per hold (see the script). A stock key that no longer exists is not recreated.
func Release(ctx context.Context, rdb goredis.Scripter, ev rediskeys.Event, categories []CategoryQty, niks []string) (ReleaseResult, error) {
	if len(categories) == 0 {
		return ReleaseResult{}, errors.New("redisscript: Release needs at least one category")
	}
	keys := make([]string, 0, len(categories)+len(niks))
	argv := []any{len(categories)}
	for _, c := range categories {
		if c.Qty < 1 {
			return ReleaseResult{}, fmt.Errorf("redisscript: Release: quantity of %q must be at least 1, got %d", c.Category, c.Qty)
		}
		keys = append(keys, ev.Stock(c.Category))
		argv = append(argv, c.Qty)
	}
	for _, h := range niks {
		keys = append(keys, ev.NIKUsed(h))
	}
	raw, err := releaseScript.Run(ctx, rdb, keys, argv...).Result()
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("redisscript: release_hold: %w", err)
	}
	r, ok := raw.([]any)
	if !ok || len(r) != 2 {
		return ReleaseResult{}, fmt.Errorf("redisscript: release_hold: unexpected reply %v", raw)
	}
	deleted, ok1 := r[0].(int64)
	missing, ok2 := r[1].(int64)
	if !ok1 || !ok2 {
		return ReleaseResult{}, fmt.Errorf("redisscript: release_hold: unexpected reply %v", raw)
	}
	return ReleaseResult{NIKsDeleted: int(deleted), StockMissing: int(missing)}, nil
}
