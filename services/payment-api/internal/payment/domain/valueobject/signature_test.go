package valueobject

import (
	"crypto/sha512"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The hash is plain concatenation in a fixed order, with no separators. Pinned
// against an independently computed SHA512 rather than against SignatureKey's
// own output, so a change to the concatenation order fails here.
func TestSignatureKey_MatchesSpec(t *testing.T) {
	order, status, amount, key := "RB-00912:1", "200", "3000000.00", "SB-Mid-server-xyz"
	sum := sha512.Sum512([]byte("RB-00912:1" + "200" + "3000000.00" + "SB-Mid-server-xyz"))
	assert.Equal(t, hex.EncodeToString(sum[:]), SignatureKey(order, status, amount, key))
}

func TestSignatureKey_IsHexSHA512(t *testing.T) {
	got := SignatureKey("RB-1:1", "200", "1000.00", "k")
	assert.Len(t, got, 128)
	assert.Equal(t, strings.ToLower(got), got)
}

// gross_amount enters the hash exactly as received. "3000000" and
// "3000000.00" are the same money and different signatures, so normalising the
// value before hashing would reject every real notification.
func TestSignatureKey_AmountNotNormalised(t *testing.T) {
	assert.NotEqual(t,
		SignatureKey("RB-1:1", "200", "3000000", "k"),
		SignatureKey("RB-1:1", "200", "3000000.00", "k"),
	)
}

func TestVerifySignature(t *testing.T) {
	const key = "SB-Mid-server-xyz"
	want := SignatureKey("RB-00912:1", "200", "3000000.00", key)

	assert.True(t, VerifySignature(want, "RB-00912:1", "200", "3000000.00", key))

	t.Run("wrong server key", func(t *testing.T) {
		assert.False(t, VerifySignature(want, "RB-00912:1", "200", "3000000.00", "other"))
	})
	t.Run("wrong order id", func(t *testing.T) {
		assert.False(t, VerifySignature(want, "RB-00912:2", "200", "3000000.00", key))
	})
	t.Run("wrong status code", func(t *testing.T) {
		assert.False(t, VerifySignature(want, "RB-00912:1", "201", "3000000.00", key))
	})
	t.Run("wrong amount", func(t *testing.T) {
		assert.False(t, VerifySignature(want, "RB-00912:1", "200", "3000000.01", key))
	})
	t.Run("empty signature", func(t *testing.T) {
		assert.False(t, VerifySignature("", "RB-00912:1", "200", "3000000.00", key))
	})
	t.Run("truncated signature", func(t *testing.T) {
		assert.False(t, VerifySignature(want[:127], "RB-00912:1", "200", "3000000.00", key))
	})
	t.Run("uppercase hex is not accepted", func(t *testing.T) {
		// The comparison is over bytes, not hex values. Midtrans sends lower
		// case; accepting either would mean decoding first, which is a wider
		// surface for no benefit.
		assert.False(t, VerifySignature(strings.ToUpper(want), "RB-00912:1", "200", "3000000.00", key))
	})
}
