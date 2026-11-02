package dto

// TicketRequest is one requested seat in a booking
type TicketRequest struct {
	CategoryID  string `json:"category_id" validate:"required"`
	NIK         string `json:"nik" validate:"required,len=16,numeric"`
	HolderEmail string `json:"holder_email" validate:"required,email"`
}

// CreateBookingRequest is the inbound payload for POST /bookings
type CreateBookingRequest struct {
	EventID      string          `json:"event_id" validate:"required"`
	Tickets      []TicketRequest `json:"tickets" validate:"required,min=1,max=3,dive"`
	ContactEmail string          `json:"contact_email" validate:"required,email"`
	TermsVersion string          `json:"terms_version" validate:"required"`
}
