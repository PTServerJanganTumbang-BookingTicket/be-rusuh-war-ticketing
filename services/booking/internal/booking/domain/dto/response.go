package dto

// Meta is the common response envelope header.
type Meta struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Envelope is the consistent {meta, data} response wrapper.
type Envelope struct {
	Meta Meta `json:"meta"`
	Data any  `json:"data,omitempty"`
}

// EventSummary is the embedded event block on a booking.).
type EventSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Date  string `json:"date"`
	Venue string `json:"venue"`
}

// TicketResponse is one seat in the booking response./§4.4).
type TicketResponse struct {
	Category  string `json:"category"`
	Price     int64  `json:"price"`
	NIKMasked string `json:"nik_masked"`
}

// Summary is the price summary block.).
type Summary struct {
	Subtotal   int64 `json:"subtotal"`
	ServiceFee int64 `json:"service_fee"`
	Total      int64 `json:"total"`
}

// PaymentTTL is the payment window block., BR-05).
type PaymentTTL struct {
	ExpiresAt  string `json:"expires_at"`
	TTLSeconds int    `json:"ttl_seconds"`
	ServerTime string `json:"server_time"`
}

// BookingResponse is the data payload for POST /bookings and GET /bookings/{id}.
type BookingResponse struct {
	BookingID   string           `json:"booking_id"`
	Status      string           `json:"status"`
	Event       EventSummary     `json:"event"`
	Tickets     []TicketResponse `json:"tickets"`
	Summary     Summary          `json:"summary"`
	PaymentTTL  PaymentTTL       `json:"payment_ttl"`
	RedirectURL string           `json:"redirect_url,omitempty"`
}
