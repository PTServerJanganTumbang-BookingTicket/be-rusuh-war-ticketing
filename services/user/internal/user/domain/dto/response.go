package dto

// RegisterResponse is returned by POST /auth/register.
type RegisterResponse struct {
	UserID string `json:"user_id"`
}

// VerifyEmailResponse is returned by GET /auth/verify-email (contract §1.7).
//
// One field, and it is always true: reaching this point means the token was
// redeemed. It exists so the client reads a value rather than inferring
// success from the status code, which is what an empty data {} would force.
type VerifyEmailResponse struct {
	EmailVerified bool `json:"email_verified"`
}

// VerifyMagicLinkResponse is returned by GET /auth/verify.
type VerifyMagicLinkResponse struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	ExpiresIn    int         `json:"expires_in"`
	User         UserSummary `json:"user"`

	// RedirectContext restores the page the customer was on before logging in
	// (BR-09). Nil when the account has nothing in flight, which is the
	// ordinary case — the field is omitted rather than sent empty so the
	// frontend's check is "is it there", not "is it populated".
	RedirectContext *RedirectContext `json:"redirect_context,omitempty"`
}

// RefreshSessionResponse is returned by POST /auth/refresh.
//
// A new refresh token comes back with every call: tokens rotate, and the one
// just presented is spent. Clients must replace their stored copy or their
// next refresh will look like a replay and end the session.
type RefreshSessionResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// UserSummary is the condensed user info embedded in auth responses.
type UserSummary struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	// NIK is the MASKED form. The full sixteen digits are never returned by
	// any endpoint (FR-32c). Empty until registration binds one.
	NIK string `json:"nik,omitempty"`
}

// RedirectContext carries re-entrancy information (BR-09).
type RedirectContext struct {
	Type      string `json:"type,omitempty"`
	EventID   string `json:"event_id,omitempty"`
	BookingID string `json:"booking_id,omitempty"`
	Message   string `json:"message,omitempty"`
}

// AccountProfileResponse is returned by GET /accounts/me (contract §1.8).
//
// Two fields, and there is no third. In particular there is no name: the
// account does not store one, because Dukcapil validates the NIK alone and a
// name is collected at no point in this system (FR-15a).
//
// NIKMasked is sent even when empty rather than omitted, so the profile page
// reads one shape on every account. An account can exist without a bound NIK —
// a magic-link sign-in creates one before registration finishes — and for that
// account the field is "", which the page renders as "not yet verified". An
// omitted field would make the client tell that apart by absence instead.
type AccountProfileResponse struct {
	Email string `json:"email"`
	// NIKMasked is the MASKED NIK Terdaftar. The full sixteen digits are never
	// returned by any endpoint (FR-32c), and the field is named for the mask so
	// that a client author cannot mistake it for something it can send back as
	// a `nik` (contract §1.8).
	NIKMasked string `json:"nik_masked"`
}

// SavedNIKsResponse is returned by GET /accounts/me/saved-niks.
type SavedNIKsResponse struct {
	MaxEntries int            `json:"max_entries"`
	SavedNIKs  []SavedNIKItem `json:"saved_niks"`
}

// SavedNIKItem is a single entry in the saved-NIK list.
//
// There is no field for the full NIK, by construction: this service only ever
// holds the mask and the hash, so no change to a handler can start returning
// one (FR-32c).
type SavedNIKItem struct {
	ID         string `json:"id"`
	NIKMasked  string `json:"nik_masked"`
	Label      string `json:"label"`
	VerifiedAt string `json:"verified_at"` // RFC3339
	Status     string `json:"status"`      // always VERIFIED — no unverified rows exist
}

// MyTicketsResponse is returned by GET /users/me/tickets.
//
// Total travels with the rows so the handler can emit meta.pagination; clients
// page on it and have no other source for the row count (contract §6.1).
type MyTicketsResponse struct {
	Total   int            `json:"-"`
	Limit   int            `json:"-"`
	Offset  int            `json:"-"`
	Tickets []MyTicketItem `json:"tickets"`
}

// MyTicketItem is a single row in the ticket list.
type MyTicketItem struct {
	TicketID   string       `json:"ticket_id"`
	BookingID  string       `json:"booking_id"`
	Event      EventSummary `json:"event"`
	Category   string       `json:"category"`
	Price      int64        `json:"price"`
	Status     string       `json:"status"`
	ETicketURL string       `json:"eticket_url"`
}

// EventSummary is the condensed event info embedded in ticket responses.
type EventSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Date     string `json:"date"`
	Venue    string `json:"venue"`
	ImageURL string `json:"image_url"`
}

// ETicketResponse is returned by GET /tickets/{ticket_id}/eticket.
type ETicketResponse struct {
	TicketID     string        `json:"ticket_id"`
	Event        ETicketEvent  `json:"event"`
	Category     string        `json:"category"`
	Holder       ETicketHolder `json:"holder"`
	QRCode       string        `json:"qr_code"`
	QRValue      string        `json:"qr_value"`
	Instructions string        `json:"instructions"`
}

// ETicketEvent is the event summary on an e-ticket.
type ETicketEvent struct {
	Name  string `json:"name"`
	Date  string `json:"date"`
	Time  string `json:"time"`
	Venue string `json:"venue"`
}

// ETicketHolder identifies the person the ticket admits (FR-27).
//
// Masked only. The gate matches the attendee's physical KTP by hashing what
// they present; it never needs the digits from here.
type ETicketHolder struct {
	NIKMasked string `json:"nik_masked"`
}
