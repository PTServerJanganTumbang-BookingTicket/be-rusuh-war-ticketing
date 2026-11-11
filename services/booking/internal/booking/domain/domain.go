// Package domain holds the booking entities the use cases work with.
package domain

import (
	"errors"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
)

// Contract codes used by booking (api-contracts Appendix A).
const (
	CodeNotFound             = "NOT_FOUND"
	CodeStockUninitialized   = "STOCK_UNINITIALIZED"
	CodeStockNegative        = "STOCK_NEGATIVE"
	CodeBookingStatusInvalid = "BOOKING_STATUS_INVALID"
	CodeBookingExpired       = "BOOKING_EXPIRED"
	CodeQtyExceeded          = "QTY_EXCEEDED"
	CodeInsufficientStock    = "INSUFFICIENT_STOCK"
	CodeTicketsUnavailable   = "TICKETS_UNAVAILABLE"
	CodeNIKAlreadyUsed       = "NIK_ALREADY_USED"
)

// PaymentWindow is how long a held booking waits for payment: payment_expires_at = held_at + 10 minutes (BR-05).
const PaymentWindow = 10 * time.Minute

// Metric names.
const (
	// MetricLostGrant counts a live grant hash whose booking row does not exist: admission-ctl writes the row first,
	// so this means data loss or a bug.
	MetricLostGrant = "LostGrant"
	// MetricStockUninitialized counts a ticket page that found a stock key missing in Redis.
	MetricStockUninitialized = "StockUninitialized"
	// MetricUnknownPaymentMethod counts a paid booking whose payment_method has no display label yet.
	MetricUnknownPaymentMethod = "UnknownPaymentMethod"
	// MetricPaymentDeadlineMissing counts a pending-payment booking without payment_expires_at (a data bug).
	MetricPaymentDeadlineMissing = "PaymentDeadlineMissing"
	// MetricHoldCompensated counts a hold given back because the booking transaction failed after it.
	MetricHoldCompensated = "HoldCompensated"
	// MetricHoldCompensationFailed counts a hold that could not be given back (Redis down): stock and NIKs stay held
	// without a booking until stock reconciliation (B5) repairs them.
	MetricHoldCompensationFailed = "HoldCompensationFailed"
	// MetricHoldUnknown counts a hold whose answer never arrived (Redis error): nothing is released, because
	// releasing a hold that was not taken would add stock out of nothing.
	MetricHoldUnknown = "HoldUnknown"
	// MetricHoldCommitUnknown counts a commit whose result is unknown: the hold is kept, because releasing the stock
	// of a booking that did commit would oversell. Better to under-sell until reconciliation.
	MetricHoldCommitUnknown = "HoldCommitUnknown"
	// MetricReleaseStockMissing counts a release that found its stock key gone (not recreated; B5 restores it).
	MetricReleaseStockMissing = "ReleaseStockMissing"
	// MetricStockNegative counts the last guard of the hold script firing: the stock would have gone below zero.
	MetricStockNegative = "StockNegative"
	// MetricNIKClaimConflict counts nik_claims refusing a NIK that Redis had let through (a flush or failover).
	MetricNIKClaimConflict = "NIKClaimConflict"
)

// ErrNIKUnavailable means the NIK check could not be completed: the invocation failed or timed out, nik-verify or
// Dukcapil could not answer, or the answer was unusable. POST /bookings answers 503 DUKCAPIL_UNAVAILABLE: no hold
// without proof (fail closed). It is not "the NIK is invalid".
var ErrNIKUnavailable = errors.New("booking: NIK check unavailable")

// NIKCheck is nik-verify's answer for one typed NIK. NIKHmac and NIKMasked are what booking stores
// (booking_items.nik_masked, nik_claims.nik_hmac); the full NIK is never kept. Valid false is an answer: Status says
// why (NOT_FOUND, INACTIVE).
type NIKCheck struct {
	NIKHmac   string
	NIKMasked string
	Valid     bool
	Status    string
	Source    string // registered | saved | verified
}

// Booking is the part of a bookings row the ticket page needs.
type Booking struct {
	ID               string
	EventID          string
	Status           bookingstate.Status
	BookingExpiresAt time.Time
	PaymentExpiresAt time.Time // zero before HELD
}

// Event is the catalog row rendered at the top of the page.
type Event struct {
	ID       string
	Name     string
	Date     string // YYYY-MM-DD, as the contract shows it
	Venue    string
	ImageURL string
}

// Account is what the ticket page shows of a logged-in customer: the email that pre-fills contact_email and the
// masked NIK offered for the first seat. NIKMasked is nil when the account has no verified NIK. The full NIK and its
// hash are never read (NFR-11).
type Account struct {
	Email     string  `json:"email"`
	NIKMasked *string `json:"nik_masked"`
}

// Category is one ticket category of an event, in display order.
type Category struct {
	ID          string
	Name        string
	Description string
	Price       int64
	MaxPerOrder int
}

// BookingDetail is everything GET /bookings/{id} renders: the booking, its event and one item per seat.
type BookingDetail struct {
	ID               string
	EventID          string
	Status           bookingstate.Status
	PaymentExpiresAt time.Time // zero before HELD
	PaymentMethod    string    // bank_transfer | gopay | "" (set by payment-api)
	Event            Event
	Items            []Item
}

// Item is one seat (booking_items), with its ticket once issued.
type Item struct {
	ID           string
	CategoryName string
	UnitPrice    int64
	NIKMasked    string
	TicketID     string // "" until the ticket is issued
	TicketStatus string // VALID | USED | EXPIRED | REVOKED; "" until issued
}

// BookingStatus is what POST /bookings reads of a booking before it looks at the body: the status and whether the
// 5-minute deadline has passed on Aurora's clock.
type BookingStatus struct {
	Status       bookingstate.Status
	PastDeadline bool
}

// Identity is the stored proof of a NIK of an account: the hash and the masked form, as they were verified.
type Identity struct {
	NIKHmac   string
	NIKMasked string
}

// Identities are the NIKs of the logged-in account that a seat may use: its own registered NIK (nil without a
// verified one) and the saved NIKs asked for, by id. An id of another account or a deleted one is simply absent.
type Identities struct {
	Own   *Identity
	Saved map[string]Identity
}

// Seat is one seat of an order, ready to be stored: the category as bought and the NIK as verified.
type Seat struct {
	ID           string
	CategoryID   string
	CategoryName string
	UnitPrice    int64
	NIKHmac      string
	NIKMasked    string
}

// Placement is everything the booking transaction writes.
type Placement struct {
	BookingID    string
	GrantToken   string
	EventID      string
	ContactEmail string
	Seats        []Seat
}

// CategoryQty is the number of seats wanted in one category.
type CategoryQty struct {
	CategoryID string
	Qty        int64
}

// HoldOrder is what the hold script takes: seats per category (once each) and one NIK hash per seat, in seat order.
type HoldOrder struct {
	Categories []CategoryQty
	NIKs       []string
}

// Hold answers (docs/lua-scripts.md §4).
const (
	HoldHeld               = "HELD"
	HoldNoStock            = "NO_STOCK"
	HoldStockUninitialized = "STOCK_UNINITIALIZED"
	HoldNIKTaken           = "NIK_TAKEN"
	HoldStockNegative      = "STOCK_NEGATIVE"
)

// HoldAnswer is the hold script's answer. Index is the category (NO_STOCK, STOCK_UNINITIALIZED, STOCK_NEGATIVE)
// or the seat (NIK_TAKEN), from 0, in the order of the HoldOrder.
type HoldAnswer struct {
	Code  string
	Index int
}

// Taken reports whether the seats and NIK locks are now held.
func (a HoldAnswer) Taken() bool { return a.Code == HoldHeld }

// PlaceOutcome is how the booking transaction ended.
type PlaceOutcome int

// Outcomes of Orders.Place.
const (
	// Placed: the booking is HELD with its seats and NIK claims.
	Placed PlaceOutcome = iota + 1
	// AlreadyPlaced: the booking was already HELD or later; nothing was written (an idempotent retry).
	AlreadyPlaced
	// Gone: no such booking for this grant, or it ended (LAPSED, EXPIRED, ABANDONED).
	Gone
	// NotOpened: still GRANTED, within its time: the ticket page was never opened.
	NotOpened
	// TimeOver: GRANTED or SELECTING with the 5 minutes over.
	TimeOver
	// Refused: the hold was refused (see Place's HoldAnswer); the transaction committed no change.
	Refused
	// ClaimConflict: nik_claims refused a NIK Redis had let through; the transaction rolled back.
	ClaimConflict
)

// PlaceResult is the result of Orders.Place. Hold is set for Refused; ConflictIndex for ClaimConflict.
type PlaceResult struct {
	Outcome       PlaceOutcome
	Hold          HoldAnswer
	ConflictIndex int
}

// QueueSnapshot is what the sold-out answer reports of the queue: its state label and when it filled.
type QueueSnapshot struct {
	State        string    // the queue:{ev} state field, "" when unreadable
	FullBookedAt time.Time // zero when not written yet
}
