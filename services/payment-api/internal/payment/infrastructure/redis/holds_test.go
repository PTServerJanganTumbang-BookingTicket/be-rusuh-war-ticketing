package redis

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

func setupMiniredis(t *testing.T) (*miniredis.Miniredis, *goredis.Client) {
	t.Helper()
	s, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(s.Close)

	rdb := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return s, rdb
}

func TestRelease_SeatsAndNIKs_OneAtomicCall(t *testing.T) {
	s, rdb := setupMiniredis(t)
	ctx := context.Background()
	eventID := "EV-123"

	// Set up stock keys
	ev, err := rediskeys.NewEvent(eventID)
	require.NoError(t, err)
	require.NoError(t, s.Set(ev.Stock("CAT-A"), "10"))
	require.NoError(t, s.Set(ev.Stock("CAT-B"), "5"))
	require.NoError(t, s.Set(ev.NIKUsed("nik-hmac-1"), "1"))
	require.NoError(t, s.Set(ev.NIKUsed("nik-hmac-2"), "1"))

	holds := NewHolds(rdb)
	seats := []portout.SeatCount{
		{CategoryID: "CAT-A", Qty: 2},
		{CategoryID: "CAT-B", Qty: 1},
	}
	niks := []string{"nik-hmac-1", "nik-hmac-2"}

	missing, err := holds.Release(ctx, eventID, seats, niks)
	require.NoError(t, err)
	assert.Equal(t, 0, missing)

	// Verify stock was incremented
	catA, err := s.Get(ev.Stock("CAT-A"))
	require.NoError(t, err)
	assert.Equal(t, "12", catA)
	catB, err := s.Get(ev.Stock("CAT-B"))
	require.NoError(t, err)
	assert.Equal(t, "6", catB)

	// Verify NIK keys were deleted
	assert.False(t, s.Exists(ev.NIKUsed("nik-hmac-1")))
	assert.False(t, s.Exists(ev.NIKUsed("nik-hmac-2")))
}

func TestRelease_NIKsWithoutSeats_DeletesTheKeys(t *testing.T) {
	s, rdb := setupMiniredis(t)
	ctx := context.Background()
	eventID := "EV-123"

	ev, err := rediskeys.NewEvent(eventID)
	require.NoError(t, err)
	require.NoError(t, s.Set(ev.NIKUsed("nik-hmac-1"), "1"))
	require.NoError(t, s.Set(ev.NIKUsed("nik-hmac-2"), "1"))

	holds := NewHolds(rdb)
	// No seats, only NIKs - the edge case F3 fixes
	missing, err := holds.Release(ctx, eventID, nil, []string{"nik-hmac-1", "nik-hmac-2"})
	require.NoError(t, err)
	assert.Equal(t, 0, missing)

	// Verify NIK keys were deleted
	assert.False(t, s.Exists(ev.NIKUsed("nik-hmac-1")), "NIK key should be deleted")
	assert.False(t, s.Exists(ev.NIKUsed("nik-hmac-2")), "NIK key should be deleted")
}

func TestRelease_Nothing_MakesNoRedisCall(t *testing.T) {
	_, rdb := setupMiniredis(t)
	ctx := context.Background()

	holds := NewHolds(rdb)

	// No seats, no NIKs
	missing, err := holds.Release(ctx, "EV-123", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, missing)

	// Empty slices
	missing, err = holds.Release(ctx, "EV-123", []portout.SeatCount{}, []string{})
	require.NoError(t, err)
	assert.Equal(t, 0, missing)
}

func TestRelease_MissingStockKeys_CountsStockMissing(t *testing.T) {
	s, rdb := setupMiniredis(t)
	ctx := context.Background()
	eventID := "EV-123"

	ev, err := rediskeys.NewEvent(eventID)
	require.NoError(t, err)
	// Only set one of two stock keys
	require.NoError(t, s.Set(ev.Stock("CAT-A"), "10"))
	// CAT-B key is missing

	holds := NewHolds(rdb)
	seats := []portout.SeatCount{
		{CategoryID: "CAT-A", Qty: 2},
		{CategoryID: "CAT-B", Qty: 1},
	}

	missing, err := holds.Release(ctx, eventID, seats, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, missing, "should count the missing stock key")

	// The existing key should still be incremented
	catA, err := s.Get(ev.Stock("CAT-A"))
	require.NoError(t, err)
	assert.Equal(t, "12", catA)
}

func TestRelease_InvalidEventID_ReturnsError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	ctx := context.Background()

	holds := NewHolds(rdb)
	seats := []portout.SeatCount{{CategoryID: "CAT-A", Qty: 1}}

	_, err := holds.Release(ctx, "", seats, nil)
	assert.Error(t, err, "empty event ID should error")
}
