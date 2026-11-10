// Package redisscript loads and runs the Lua scripts of the queue domain on a Redis cluster.
//
// advance_now_serving.lua is the only script here. Its contract and every rule are in
// features/advance_now_serving.feature (ADR-006 §3.4, §8.11, §8.12).
package redisscript

import (
	"context"
	"crypto/sha1" //nolint:gosec // Redis identifies scripts by SHA1; this is not a security use.
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

//go:embed advance_now_serving.lua
var advanceSrc string

// Result codes returned by the advance script.
const (
	ResultAdmitted           = "ADMITTED"
	ResultNoop               = "NOOP"
	ResultEmpty              = "EMPTY"
	ResultStale              = "STALE"
	ResultFullBooked         = "FULL_BOOKED"
	ResultScheduled          = "SCHEDULED"
	ResultClosed             = "CLOSED"
	ResultMissing            = "MISSING"
	ResultStockUninitialized = "STOCK_UNINITIALIZED"
)

// Result is what one run of advance_now_serving.lua returns.
type Result struct {
	From   int64
	To     int64
	Code   string
	Tokens []string
}

// Args are the script's ARGV. Open and Browsing include pending grants (admission-ctl adds them).
type Args struct {
	Max        int64
	Open       int64
	Browsing   int64
	NowServing int64 // the value the controller read: the CAS fence
	Now        int64 // epoch seconds
}

// Script is the advance_now_serving script bound to a Redis client.
type Script struct {
	rdb     goredis.Scripter
	sha     string
	reloads atomic.Int32
}

// New returns the script bound to rdb. Nothing is sent until Load or Run.
func New(rdb goredis.Scripter) *Script {
	sum := sha1.Sum([]byte(advanceSrc)) //nolint:gosec // see import
	return &Script{rdb: rdb, sha: hex.EncodeToString(sum[:])}
}

// Source returns the Lua text.
func Source() string { return advanceSrc }

// Reloads counts how often a NOSCRIPT reply made Run load the script again.
func (s *Script) Reloads() int { return int(s.reloads.Load()) }

// Load registers the script on the node Redis picks and returns its SHA1.
func (s *Script) Load(ctx context.Context) (string, error) {
	sha, err := s.rdb.ScriptLoad(ctx, advanceSrc).Result()
	if err != nil {
		return "", fmt.Errorf("redisscript: load advance_now_serving: %w", err)
	}
	return sha, nil
}

// Advance runs the script for event ev with the given stock categories.
func (s *Script) Advance(ctx context.Context, ev rediskeys.Event, categories []string, a Args) (Result, error) {
	keys := []string{ev.QueueHash(), ev.ByTicket()}
	for _, c := range categories {
		keys = append(keys, ev.Stock(c))
	}
	return s.Run(ctx, keys, a.Max, a.Open, a.Browsing, a.NowServing, a.Now)
}

// Run calls the script with raw KEYS and ARGV by SHA. If Redis lost the script
// (restart, SCRIPT FLUSH, failover) it is loaded again and the call retried once.
func (s *Script) Run(ctx context.Context, keys []string, argv ...any) (Result, error) {
	raw, err := s.rdb.EvalSha(ctx, s.sha, keys, argv...).Result()
	if err != nil && strings.HasPrefix(err.Error(), "NOSCRIPT") {
		// EVAL is routed by its keys, so on a multi-shard cluster it reloads the script on the
		// shard that lost it (SCRIPT LOAD has no key and may hit another node).
		s.reloads.Add(1)
		raw, err = s.rdb.Eval(ctx, advanceSrc, keys, argv...).Result()
	}
	if err != nil {
		return Result{}, fmt.Errorf("redisscript: advance_now_serving: %w", err)
	}
	return parse(raw)
}

func parse(raw any) (Result, error) {
	r, ok := raw.([]any)
	if !ok || len(r) != 4 {
		return Result{}, fmt.Errorf("redisscript: unexpected reply %v", raw)
	}
	from, ok1 := r[0].(int64)
	to, ok2 := r[1].(int64)
	code, ok3 := r[2].(string)
	toks, ok4 := r[3].([]any)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return Result{}, fmt.Errorf("redisscript: unexpected reply %v", raw)
	}
	res := Result{From: from, To: to, Code: code}
	for _, t := range toks {
		res.Tokens = append(res.Tokens, fmt.Sprint(t))
	}
	return res, nil
}
