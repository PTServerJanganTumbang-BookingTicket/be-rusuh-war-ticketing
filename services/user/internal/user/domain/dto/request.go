package dto

// RequestMagicLinkRequest is the payload for POST /auth/magic-link and
// POST /auth/magic-link/resend.
type RequestMagicLinkRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// RegisterRequest is the payload for POST /auth/register.
//
// Consent is a pointer so an absent object is distinguishable from one that was
// sent with accepted:false. Both are refused, but with different codes: a
// missing object is a malformed request, while accepted:false is a customer who
// declined and must be told so (CONSENT_REQUIRED, contract §1.5).
type RegisterRequest struct {
	Email   string          `json:"email" validate:"required,email"`
	NIK     string          `json:"nik" validate:"required"`
	Consent *ConsentRequest `json:"consent" validate:"required"`
}

// ConsentRequest carries explicit agreement to third-party NIK processing.
// The checkbox must not be pre-checked in the UI (FR-31c).
//
// One field, deliberately. The client used to send policy_version as well, and
// it was written straight into the immutable consent record — which made the
// client the authority on which policy the customer had just agreed to, for a
// record whose whole purpose is to be trustworthy evidence. A field like that
// is worse than absent: it looks like provenance and is not. A sent value is
// now ignored rather than rejected, so an unchanged client still registers.
type ConsentRequest struct {
	Accepted bool `json:"accepted"`
}

// RefreshSessionRequest is the payload for POST /auth/refresh.
//
// The token travels in the body rather than a header: it is not a bearer
// credential for this endpoint, it is the subject of the request, and keeping
// it out of Authorization stops it being logged by proxies that record that
// header.
type RefreshSessionRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// AddSavedNIKRequest is the payload for POST /accounts/me/saved-niks.
//
// NIK carries no length tag: the raw value may arrive spaced or hyphenated, and
// nik-verify normalises before judging it. A len=16 tag here would reject input
// that is merely formatted.
type AddSavedNIKRequest struct {
	NIK   string `json:"nik" validate:"required"`
	Label string `json:"label" validate:"required,max=50"`
}

// GetMyTicketsRequest carries the query parameters for GET /users/me/tickets.
type GetMyTicketsRequest struct {
	Status string // "upcoming", "past", "all"
	Limit  int
	Offset int
}
