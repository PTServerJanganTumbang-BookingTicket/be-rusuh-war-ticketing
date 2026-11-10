package redisscript

import (
	"context"
	"errors"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

// fakeScripter answers EvalSha/Eval/ScriptLoad from canned replies; every other Scripter method panics (nil embed).
type fakeScripter struct {
	goredis.Scripter
	evalShaReply, evalReply *goredis.Cmd
	loadReply               *goredis.StringCmd
	evalShaKeys, evalKeys   []string
	evalShaArgs             []any
	evalShaCalls, evalCalls int
}

func (f *fakeScripter) EvalSha(ctx context.Context, _ string, keys []string, args ...any) *goredis.Cmd {
	f.evalShaCalls++
	f.evalShaKeys, f.evalShaArgs = keys, args
	return f.evalShaReply
}

func (f *fakeScripter) Eval(ctx context.Context, _ string, keys []string, _ ...any) *goredis.Cmd {
	f.evalCalls++
	f.evalKeys = keys
	return f.evalReply
}

func (f *fakeScripter) ScriptLoad(context.Context, string) *goredis.StringCmd { return f.loadReply }

func okReply(code string, toks ...any) *goredis.Cmd {
	cmd := goredis.NewCmd(context.Background())
	cmd.SetVal([]any{int64(3), int64(5), code, toks})
	return cmd
}

func errReply(msg string) *goredis.Cmd {
	cmd := goredis.NewCmd(context.Background())
	cmd.SetErr(errors.New(msg))
	return cmd
}

func TestSource_returnsTheEmbeddedLua(t *testing.T) {
	assert.Contains(t, Source(), "redis.call")
}

func TestNew_hashesTheEmbeddedScript(t *testing.T) {
	s := New(&fakeScripter{})
	assert.Len(t, s.sha, 40)
	assert.Zero(t, s.Reloads())
}

func TestLoad_returnsTheShaRedisReports(t *testing.T) {
	f := &fakeScripter{loadReply: goredis.NewStringResult("abc123", nil)}
	sha, err := New(f).Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "abc123", sha)
}

func TestLoad_wrapsTheRedisError(t *testing.T) {
	f := &fakeScripter{loadReply: goredis.NewStringResult("", errors.New("boom"))}
	_, err := New(f).Load(context.Background())
	assert.ErrorContains(t, err, "load advance_now_serving")
}

func TestAdvance_passesQueueByTicketAndStockKeysThenTheArgs(t *testing.T) {
	f := &fakeScripter{evalShaReply: okReply(ResultAdmitted, "t1", "t2")}
	ev, err := rediskeys.NewEvent("evt-1")
	require.NoError(t, err)

	res, err := New(f).Advance(context.Background(), ev, []string{"VIP", "REG"},
		Args{Max: 10, Open: 4, Browsing: 2, NowServing: 3, Now: 1000})

	require.NoError(t, err)
	assert.Equal(t, Result{From: 3, To: 5, Code: ResultAdmitted, Tokens: []string{"t1", "t2"}}, res)
	assert.Equal(t, []string{ev.QueueHash(), ev.ByTicket(), ev.Stock("VIP"), ev.Stock("REG")}, f.evalShaKeys)
	assert.Equal(t, []any{int64(10), int64(4), int64(2), int64(3), int64(1000)}, f.evalShaArgs)
}

func TestRun_loadsTheScriptAgainOnceWhenRedisLostIt(t *testing.T) {
	f := &fakeScripter{
		evalShaReply: errReply("NOSCRIPT No matching script"),
		evalReply:    okReply(ResultNoop),
	}
	s := New(f)

	res, err := s.Run(context.Background(), []string{"k"})

	require.NoError(t, err)
	assert.Equal(t, ResultNoop, res.Code)
	assert.Equal(t, 1, s.Reloads())
	assert.Equal(t, 1, f.evalCalls)
	assert.Equal(t, []string{"k"}, f.evalKeys)
}

func TestRun_wrapsOtherRedisErrorsWithoutRetrying(t *testing.T) {
	f := &fakeScripter{evalShaReply: errReply("CLUSTERDOWN")}
	s := New(f)

	_, err := s.Run(context.Background(), []string{"k"})

	assert.ErrorContains(t, err, "CLUSTERDOWN")
	assert.Zero(t, f.evalCalls)
	assert.Zero(t, s.Reloads())
}

func TestRun_returnsTheErrorWhenTheReloadFailsToo(t *testing.T) {
	f := &fakeScripter{evalShaReply: errReply("NOSCRIPT"), evalReply: errReply("down")}
	_, err := New(f).Run(context.Background(), []string{"k"})
	assert.ErrorContains(t, err, "down")
}

func TestParse_rejectsMalformedReplies(t *testing.T) {
	for name, raw := range map[string]any{
		"not a list":      "OK",
		"wrong length":    []any{int64(1)},
		"from not int":    []any{"x", int64(1), "A", []any{}},
		"to not int":      []any{int64(1), "x", "A", []any{}},
		"code not string": []any{int64(1), int64(1), 7, []any{}},
		"tokens not list": []any{int64(1), int64(1), "A", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parse(raw)
			assert.ErrorContains(t, err, "unexpected reply")
		})
	}
}

func TestParse_stringifiesTokens(t *testing.T) {
	res, err := parse([]any{int64(1), int64(2), ResultAdmitted, []any{"a", int64(7)}})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "7"}, res.Tokens)
}
