package valueobject

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"3171000000000022":     "3171000000000022",
		"3171 0000 0000 0022":  "3171000000000022",
		"3171-0000-0000-0022":  "3171000000000022",
		"  3171000000000022  ": "3171000000000022",
		"3171000000000022\n":   "3171000000000022",
		"abc":                  "",
		"":                     "",
	}
	for in, want := range cases {
		assert.Equal(t, want, Normalize(in), "input %q", in)
	}
}

func TestNormalize_MakesFormattingIrrelevantToTheHash(t *testing.T) {
	// The reason Normalize exists. A NIK hashed two different ways is a BR-02
	// hole that cannot be repaired, because the pepper cannot be rotated and
	// the plaintext was never stored.
	spaced := Hash(Normalize("3171 0000 0000 0022"), "pepper")
	plain := Hash(Normalize("3171000000000022"), "pepper")
	assert.Equal(t, plain, spaced)
}

func TestIsValidFormat(t *testing.T) {
	assert.True(t, IsValidFormat("3171000000000022"))
	assert.False(t, IsValidFormat("317100000000002"))
	assert.False(t, IsValidFormat("31710000000000222"))
	assert.False(t, IsValidFormat(""))
}

func TestHash_DeterministicAndKeyed(t *testing.T) {
	a := Hash("3171000000000022", "pepper-one")
	assert.Equal(t, a, Hash("3171000000000022", "pepper-one"), "same input and pepper must agree")
	assert.NotEqual(t, a, Hash("3171000000000023", "pepper-one"), "different NIK must differ")
	assert.NotEqual(t, a, Hash("3171000000000022", "pepper-two"), "different pepper must differ")
	assert.Len(t, a, 64, "hex-encoded SHA-256")
}

func TestHash_DoesNotContainTheNIK(t *testing.T) {
	assert.NotContains(t, Hash("3171000000000022", "pepper"), "3171")
}

func TestMask(t *testing.T) {
	assert.Equal(t, "3171**********22", Mask("3171000000000022"))
	assert.Len(t, Mask("3171000000000022"), Length)
}

func TestMask_FullyMasksMalformedInput(t *testing.T) {
	// Returning the input unchanged would echo a malformed NIK verbatim into a
	// response that is supposed to never carry one.
	assert.Equal(t, strings.Repeat("*", Length), Mask("123"))
	assert.Equal(t, strings.Repeat("*", Length), Mask(""))
}
