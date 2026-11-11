package valueobject

import (
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
)

// SignatureKey is the Midtrans Core API notification signature:
//
//	SHA512(order_id + status_code + gross_amount + SignatureSecretKey)
//
// Plain concatenation, no separators.
//
// Four details that each silently break verification if got wrong, the first
// from Midtrans's Access Keys page and the rest from api-contracts §5.5:
//
//  1. The last term is the SIGNATURE SECRET KEY, not the server key. Midtrans
//     split the two on 28 October 2026. A merchant who integrated earlier
//     starts with both values identical, so the server key appears to work
//     here and stops the moment either key is regenerated - and Server Keys
//     now expire. The failure is silent: charges keep succeeding while every
//     notification is read as a forgery.
//
//  2. There is NO signature header. Midtrans sends no X-Callback-Signature of
//     any kind on Core API HTTP notifications; the signature is the
//     signature_key field inside the body.
//
//  3. The scheme is SHA512, not SHA256. The SHA256withRSA scheme in Midtrans's
//     Security Specification page belongs to other API lines, not this webhook.
//
//  4. gross_amount enters the hash EXACTLY as received, decimals included.
//     "3000000.00" and "3000000" produce different hashes and only the first
//     matches, so the value must not be normalised, parsed or reformatted
//     before hashing.
func SignatureKey(orderID, statusCode, grossAmount, signatureSecretKey string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + signatureSecretKey))
	return hex.EncodeToString(sum[:])
}

// VerifySignature reports whether a notification's signature_key is authentic.
//
// Constant time: the webhook is reachable by anyone who learns the URL, so a
// comparison that exits on the first wrong byte lets an attacker recover a
// valid signature byte by byte. A mismatch means ignore the notification
// entirely — do not process it and do not change any status.
func VerifySignature(got, orderID, statusCode, grossAmount, signatureSecretKey string) bool {
	want := SignatureKey(orderID, statusCode, grossAmount, signatureSecretKey)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
