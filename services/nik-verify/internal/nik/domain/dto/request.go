package dto

// ValidateRequest is the payload for POST /nik/validate.
//
// Only the NIK. No name, no date of birth — the form does not collect them and
// Dukcapil is not sent them (FR-15a).
//
// Length is not asserted here: the raw value may carry spaces or dashes, so
// normalisation runs first and the use case validates the result. A `len=16`
// tag would reject input that is merely formatted.
type ValidateRequest struct {
	NIK string `json:"nik" validate:"required"`
}
