// Package valueobject holds the NIK value rules: how a NIK is normalised,
// hashed and masked. These three operations are the only things in the system
// that ever touch a raw NIK, and they all run in-process and discard it.
package valueobject

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/nikformat"
)

// Length is the fixed length of an Indonesian NIK.
const Length = nikformat.Length

// Normalize strips everything that is not a digit and trims surrounding space. The rule lives in pkg/nikformat, shared
// with booking, so both read a typed NIK the same way; it must run before hashing, always (see there).
func Normalize(raw string) string { return nikformat.Normalize(raw) }

// IsValidFormat reports whether the normalised NIK is syntactically usable: exactly 16 digits, checked before
// Dukcapil is called so a malformed NIK never costs a paid call (NFR-12).
func IsValidFormat(normalized string) bool { return nikformat.IsValidFormat(normalized) }

// Hash returns the keyed HMAC-SHA256 of a normalised NIK, hex-encoded.
//
// One-way by construction: holding the pepper lets you recompute a hash for a
// NIK you already have, never recover a NIK from a hash. The pepper is a
// KMS-managed secret and is NEVER rotated — rotation would change every digest
// and silently orphan every nik_claims row, and re-hashing is impossible
// because the plaintext was never stored (ADR-005 §8).
func Hash(normalized, pepper string) string {
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}

// Mask returns the display form: first four digits, then the last two.
//
// Callers persist this alongside the hash, because a hash cannot be unmasked
// and the contract requires nik_masked on saved-NIK listings and e-tickets
// (FR-32c). Inputs that are not exactly 16 digits are fully masked rather than
// returned as-is — returning the input would leak a malformed NIK verbatim.
func Mask(normalized string) string {
	if len(normalized) != Length {
		return strings.Repeat("*", Length)
	}
	return normalized[:4] + strings.Repeat("*", 10) + normalized[14:]
}
