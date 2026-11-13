package valueobject

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrderID(t *testing.T) {
	assert.Equal(t, "RB-00912_1", OrderID("RB-00912", 1))
	assert.Equal(t, "RB-00912_0", OrderID("RB-00912", 0))
	assert.Equal(t, "RB-00912_12", OrderID("RB-00912", 12))
}

// A fresh attempt must produce a different order_id, because Midtrans carries
// idempotency on order_id itself: identical means "refused", which is what
// makes a double-clicked Pay button safe and a genuine retry possible.
func TestOrderID_AttemptChangesKey(t *testing.T) {
	assert.NotEqual(t, OrderID("RB-1", 1), OrderID("RB-1", 2))
}

func TestParseOrderID_RoundTrip(t *testing.T) {
	for _, attempt := range []int{0, 1, 5, 42} {
		id, n, err := ParseOrderID(OrderID("RB-00912", attempt))
		require.NoError(t, err)
		assert.Equal(t, "RB-00912", id)
		assert.Equal(t, attempt, n)
	}
}

// The split is from the RIGHT, so a booking id that itself contains an underscore
// still parses. Splitting from the left would hand the repository a truncated
// booking id and lock the wrong row.
func TestParseOrderID_UnderscoreInBookingID(t *testing.T) {
	id, n, err := ParseOrderID("ev_RB-00912_3")
	require.NoError(t, err)
	assert.Equal(t, "ev_RB-00912", id)
	assert.Equal(t, 3, n)
}

func TestParseOrderID_Malformed(t *testing.T) {
	cases := map[string]string{
		"no underscore":        "RB-00912",
		"empty":                "",
		"leading underscore":   "_1",
		"trailing underscore":  "RB-00912_",
		"attempt not a int":    "RB-00912_abc",
		"negative attempt":     "RB-00912_-1",
		"float attempt":        "RB-00912_1.0",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := ParseOrderID(in)
			assert.ErrorIs(t, err, ErrMalformedOrderID)
		})
	}
}
