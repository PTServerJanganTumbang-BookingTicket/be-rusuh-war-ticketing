package dto

// RequestMagicLinkRequest is the payload for POST /auth/magic-link and
// POST /auth/magic-link/resend.
type RequestMagicLinkRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// RegisterRequest is the payload for POST /auth/register.
type RegisterRequest struct {
	Email   string         `json:"email" validate:"required,email"`
	NIK     string         `json:"nik" validate:"required,len=16,numeric"`
	Consent ConsentRequest `json:"consent" validate:"required"`
}

// ConsentRequest carries the user's explicit agreement to the privacy policy.
// The checkbox must not be pre-checked in the UI (FR-30).
type ConsentRequest struct {
	Accepted      bool   `json:"accepted"`
	PolicyVersion string `json:"policy_version" validate:"required"`
}

// AddSavedNIKRequest is the payload for POST /accounts/me/saved-niks.
type AddSavedNIKRequest struct {
	NIK   string `json:"nik" validate:"required,len=16,numeric"`
	Label string `json:"label" validate:"required"`
}

// GetMyTicketsRequest carries the query parameters for GET /users/me/tickets.
type GetMyTicketsRequest struct {
	Status string // "upcoming", "past", "all"
	Limit  int
	Offset int
}
