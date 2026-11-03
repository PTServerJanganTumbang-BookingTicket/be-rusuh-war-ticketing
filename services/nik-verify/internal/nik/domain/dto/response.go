package dto

// ValidateResponse is returned by POST /nik/validate.
//
// A rejected NIK is still a 200 with Valid false — the call succeeded, the
// answer was no. Non-2xx is reserved for "we could not get an answer", which is
// what callers must fail closed on.
type ValidateResponse struct {
	// NIKMasked is the display form. The full NIK is never echoed back: this
	// response crosses a service boundary and lands in logs (FR-32c).
	NIKMasked string `json:"nik_masked"`

	// NIKHmac is the keyed hash callers persist and index on.
	//
	// Returning it is what keeps the pepper inside this service. Callers store
	// what they are handed and never hold the secret, so compromising
	// services/user does not enable offline enumeration of every stored NIK —
	// the risk ADR-005 §8 accepts for every process that holds the pepper, and
	// which cannot be remediated because rotation is impossible.
	NIKHmac string `json:"nik_hmac"`

	Valid  bool   `json:"valid"`
	Status string `json:"status"`
	Source string `json:"source"` // registered | saved | verified
}
