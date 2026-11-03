package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return goredis.NewClient(&goredis.Options{Addr: mr.Addr()}), mr
}

// ── NewClient ─────────────────────────────────────────────────────────────────
// NewClient is tested in pkg/redisutil.

// ── DedupeStore ───────────────────────────────────────────────────────────────

func TestDedupeStore_MarkSentIfNew_New(t *testing.T) {
	client, _ := newTestClient(t)
	store := NewDedupeStore(client)
	isNew, err := store.MarkSentIfNew(context.Background(), "outbox-1", time.Hour)
	require.NoError(t, err)
	assert.True(t, isNew)
}

func TestDedupeStore_MarkSentIfNew_Duplicate(t *testing.T) {
	client, _ := newTestClient(t)
	store := NewDedupeStore(client)
	ctx := context.Background()
	_, _ = store.MarkSentIfNew(ctx, "outbox-dup", time.Hour)
	isNew, err := store.MarkSentIfNew(ctx, "outbox-dup", time.Hour)
	require.NoError(t, err)
	assert.False(t, isNew)
}

func TestDedupeStore_Unmark_AllowsRedelivery(t *testing.T) {
	client, _ := newTestClient(t)
	store := NewDedupeStore(client)
	ctx := context.Background()
	_, _ = store.MarkSentIfNew(ctx, "outbox-unmark", time.Hour)
	require.NoError(t, store.Unmark(ctx, "outbox-unmark"))
	isNew, err := store.MarkSentIfNew(ctx, "outbox-unmark", time.Hour)
	require.NoError(t, err)
	assert.True(t, isNew)
}

func TestDedupeStore_MarkSentIfNew_Expires(t *testing.T) {
	client, mr := newTestClient(t)
	store := NewDedupeStore(client)
	ctx := context.Background()
	_, _ = store.MarkSentIfNew(ctx, "outbox-exp", 2*time.Second)
	mr.FastForward(3 * time.Second)
	isNew, err := store.MarkSentIfNew(ctx, "outbox-exp", time.Hour)
	require.NoError(t, err)
	assert.True(t, isNew)
}
