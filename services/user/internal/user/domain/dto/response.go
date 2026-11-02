package dto

// RegisterResponse is returned by POST /auth/register.
type RegisterResponse struct {
	UserID string `json:"user_id"`
}

// VerifyMagicLinkResponse is returned by GET /auth/verify.
type VerifyMagicLinkResponse struct {
	AccessToken     string           `json:"access_token"`
	RefreshToken    string           `json:"refresh_token"`
	ExpiresIn       int              `json:"expires_in"`
	User            UserSummary      `json:"user"`
	RedirectContext *RedirectContext  `json:"redirect_context,omitempty"`
}

// UserSummary is the condensed user info embedded in auth responses.
type UserSummary struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	NIK   string `json:"nik,omitempty"` // masked; omitted when not yet set
}

// RedirectContext carries re-entrancy information so the frontend can route
// the user back to the page they were on before authentication (BR-09).
type RedirectContext struct {
	Type      string `json:"type,omitempty"`
	EventID   string `json:"event_id,omitempty"`
	BookingID string `json:"booking_id,omitempty"`
	Message   string `json:"message,omitempty"`
}

// SavedNIKsResponse is returned by GET /accounts/me/saved-niks.
type SavedNIKsResponse struct {
	MaxEntries int            `json:"max_entries"`
	SavedNIKs  []SavedNIKItem `json:"saved_niks"`
}

// SavedNIKItem is a single entry in the saved-NIK list.
// NIKMasked never returns the full 16-digit number — only the masked form.
type SavedNIKItem struct {
	ID         string `json:"id"`
	NIKMasked  string `json:"nik_masked"`
	Label      string `json:"label"`
	VerifiedAt string `json:"verified_at"` // RFC3339
	Status     string `json:"status"`       // always "VERIFIED" (no unverified rows exist)
}

// PaginationMeta is included in list endpoint responses.
type PaginationMeta struct {
	TotalData int `json:"totalData"`
	TotalPage int `json:"totalPage"`
	Limit     int `json:"limit"`
	Offset    int `json:"offset"`
}

// MyTicketsResponse is returned by GET /users/me/tickets.
type MyTicketsResponse struct {
	Pagination PaginationMeta `json:"pagination"`
	Tickets    []MyTicketItem `json:"tickets"`
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

// ETicketHolder contains the masked NIK of the person the ticket belongs to (FR-27).
type ETicketHolder struct {
	NIKMasked string `json:"nik_masked"`
}
