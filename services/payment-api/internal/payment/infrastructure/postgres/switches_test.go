package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
)

// switchDB answers the one query this adapter makes.
type switchDB struct {
	mockDB
	rows    [][]any
	err     error
	calls   int
	lastSQL string
}

func (d *switchDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.calls++
	d.lastSQL = sql
	if d.err != nil {
		return nil, d.err
	}
	return rowsOf(d.rows...), nil
}

func TestSwitches_NoRowsMeansNothingIsSwitchedOff(t *testing.T) {
	db := &switchDB{}
	off, err := NewSwitchRepository(db, 0, nil).Disabled(context.Background())
	require.NoError(t, err)
	assert.Empty(t, off)
	assert.False(t, off.MethodOff("bank_transfer"))
	assert.False(t, off.BankOff("bank_transfer", "bca"))
}

func TestSwitches_AMethodRowSwitchesTheWholeMethodOff(t *testing.T) {
	db := &switchDB{rows: [][]any{{"gopay", "", false}}}
	off, err := NewSwitchRepository(db, 0, nil).Disabled(context.Background())
	require.NoError(t, err)
	assert.True(t, off.MethodOff("gopay"))
	assert.False(t, off.MethodOff("bank_transfer"))
}

// The query COALESCEs a NULL bank_code to the empty string, which is what
// makes an empty BankCode mean "the whole method". A real bank code can never
// be empty: SupportedBank admits only the four opened codes.
func TestSwitches_ABankRowSwitchesOneBankOff(t *testing.T) {
	db := &switchDB{rows: [][]any{{"bank_transfer", "bca", false}}}
	off, err := NewSwitchRepository(db, 0, nil).Disabled(context.Background())
	require.NoError(t, err)
	assert.False(t, off.MethodOff("bank_transfer"), "the method is still payable via other banks")
	assert.True(t, off.BankOff("bank_transfer", "bca"))
	assert.False(t, off.BankOff("bank_transfer", "bni"))
}

// The query filters available = false, so an `available = true` row never
// arrives here. It exists in the log to END a switch and to record who ended
// it, and carries no meaning once it is the latest row.
func TestSwitches_TheQueryAsksOnlyForSwitchesThatAreOff(t *testing.T) {
	db := &switchDB{}
	_, err := NewSwitchRepository(db, 0, nil).Disabled(context.Background())
	require.NoError(t, err)
	assert.Contains(t, db.lastSQL, "available = false")
	assert.Contains(t, db.lastSQL, "DISTINCT ON (method_id, bank_code)")
	// Expiry is applied in SQL, so a cached answer cannot keep an expired
	// switch alive.
	assert.Contains(t, db.lastSQL, "expires_at")
	// The id tie-break is load bearing: two inserts in the same microsecond
	// would otherwise order non-deterministically and whichever row the
	// planner returned would decide whether a bank is on.
	assert.Contains(t, db.lastSQL, "changed_at DESC, id DESC")
}

func TestSwitches_CachesWithinTheTTLAndRefreshesAfter(t *testing.T) {
	db := &switchDB{rows: [][]any{{"gopay", "", false}}}
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	repo := NewSwitchRepository(db, 10*time.Second, clock)

	for i := 0; i < 5; i++ {
		_, err := repo.Disabled(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, 1, db.calls, "§5.1 is on every customer's path; one query serves the window")

	now = now.Add(11 * time.Second)
	_, err := repo.Disabled(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, db.calls, "and the switch bites once the TTL passes")
}

// A TTL of zero is no cache, which is what a debugging session wants.
func TestSwitches_ZeroTTLQueriesEveryTime(t *testing.T) {
	db := &switchDB{}
	repo := NewSwitchRepository(db, 0, nil)
	for i := 0; i < 3; i++ {
		_, err := repo.Disabled(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, 3, db.calls)
}

// The one thing a kill switch must never do is take payments down when its own
// storage is unavailable. There is no error to handle, by design.
func TestSwitches_UnreadableWithNoPriorReadMeansNothingIsOff(t *testing.T) {
	db := &switchDB{err: errors.New("pq: connection refused")}
	off, err := NewSwitchRepository(db, time.Minute, nil).Disabled(context.Background())
	require.NoError(t, err, "a read failure is not the caller's problem")
	assert.Empty(t, off)
}

// Once a state HAS been read, a later failure keeps it rather than silently
// re-enabling a bank someone switched off for a reason.
func TestSwitches_UnreadableAfterASuccessfulReadKeepsTheLastKnownState(t *testing.T) {
	db := &switchDB{rows: [][]any{{"bank_transfer", "bca", false}}}
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	repo := NewSwitchRepository(db, time.Second, func() time.Time { return now })

	off, err := repo.Disabled(context.Background())
	require.NoError(t, err)
	require.True(t, off.BankOff("bank_transfer", "bca"))

	db.err = errors.New("pq: connection refused")
	now = now.Add(time.Minute) // past the TTL, so it tries and fails

	off, err = repo.Disabled(context.Background())
	require.NoError(t, err)
	assert.True(t, off.BankOff("bank_transfer", "bca"), "BCA stays off")
}

func TestSwitches_SeveralRowsAccumulate(t *testing.T) {
	db := &switchDB{rows: [][]any{
		{"bank_transfer", "bca", false},
		{"bank_transfer", "bni", false},
		{"gopay", "", false},
	}}
	off, err := NewSwitchRepository(db, 0, nil).Disabled(context.Background())
	require.NoError(t, err)
	assert.Len(t, off, 3)
	assert.True(t, off.BankOff("bank_transfer", "bca"))
	assert.True(t, off.BankOff("bank_transfer", "bni"))
	assert.False(t, off.BankOff("bank_transfer", "bri"))
	assert.True(t, off.MethodOff("gopay"))
	// And the set is what entity.ApplyDisabled consumes, so the two halves
	// meet here rather than in a handler.
	methods := entity.ApplyDisabled(entity.Catalogue("https://cdn/icons", true), off)
	assert.Len(t, methods[0].Banks, 2)
	assert.False(t, methods[1].Enabled)
}
