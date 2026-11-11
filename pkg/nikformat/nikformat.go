// Package nikformat is the one definition of how a typed NIK is read: every character that is not a digit is dropped,
// and what is left must be exactly 16 digits. nik-verify hashes and checks the result; booking reads the same text
// before it asks nik-verify, so the on-blur check and Pay never disagree about one NIK.
//
// It holds no secret and never hashes: hashing needs NIK_PEPPER, which only nik-verify has (ADR-005 §8).
package nikformat

import "strings"

// Length is the fixed length of an Indonesian NIK.
const Length = 16

// Normalize strips everything that is not a digit and trims surrounding space.
//
// This must run before hashing, always. "3171 0000 0000 0022" and "3171000000000022" are the same identity to a human
// and to Dukcapil, but different inputs to HMAC; and because the pepper can never be rotated (ADR-005 §8), a NIK hashed
// two different ways is a BR-02 hole that cannot be repaired later.
func Normalize(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IsValidFormat reports whether the normalised NIK is syntactically usable. Format is checked before Dukcapil is
// called: a malformed NIK is the caller's mistake and must not cost a paid API call (NFR-12).
func IsValidFormat(normalized string) bool {
	return len(normalized) == Length
}
