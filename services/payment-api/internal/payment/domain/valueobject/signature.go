package valueobject

import (
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
)

// SignatureKey is the Midtrans Core API notification signature:
//
//	SHA512(order_id + status_code + gross_amount + ServerKey)
//
// Plain concatenation, no separators.
//
// Three details that each silently break verification if got wrong, all from
// api-contracts §5.5:
//
//  1. There is NO signature header. Midtrans sends no X-Callback-Signature of
//     any kind on Core API HTTP notifications; the signature is the
//     signature_key field inside the body.
//  2. The scheme is SHA512, not SHA256. The SHA256withRSA scheme in Midtrans's
//     Security Specification page belongs to other API lines, not this webhook.
//  3. gross_amount enters the hash EXACTLY as received, decimals included.
//     "3000000.00" and "3000000" produce different hashes and only the first
//     matches, so the value must not be normalised, parsed or reformatted
//     before hashing.
func SignatureKey(orderID, statusCode, grossAmount, serverKey string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + serverKey))
	return hex.EncodeToString(sum[:])
}

// VerifySignature reports whether a notification's signature_key is authentic.
//
// Constant time: the webhook is reachable by anyone who learns the URL, so a
// comparison that exits on the first wrong byte lets an attacker recover a
// valid signature byte by byte. A mismatch means ignore the notification
// entirely — do not process it and do not change any status.
func VerifySignature(got, orderID, statusCode, grossAmount, serverKey string) bool {
	want := SignatureKey(orderID, statusCode, grossAmount, serverKey)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
