// Package domain holds the queue's value types and error codes.
package domain

import "time"

// SaleState is queue:{ev}:state.
type SaleState string

// Sale states (api-contracts §3.2). Anything else is "not seeded".
const (
	StateScheduled  SaleState = "SCHEDULED"
	StateOpen       SaleState = "OPEN"
	StateFullBooked SaleState = "FULL_BOOKED"
	StateClosed     SaleState = "CLOSED"
)

// Known reports whether s is one of the four sale states.
func (s SaleState) Known() bool {
	switch s {
	case StateScheduled, StateOpen, StateFullBooked, StateClosed:
		return true
	}
	return false
}

// EventSnapshot is what join reads about an event in one round trip.
type EventSnapshot struct {
	State        SaleState // "" when queue:{ev}:state is missing
	NowServing   int64
	Issued       int64
	Name         string // queue:{ev} field "name", seeded by sale-ops
	SaleStartsAt string // queue:{ev} field "sale_starts_at" (RFC 3339)
	FullBookedAt string // queue:{ev}:full_booked_at
}

// Depth is how many issued numbers are still waiting.
func (s EventSnapshot) Depth() int64 { return s.Issued - s.NowServing }

// Engagement is token:<queue_token>: one customer's place for one event.
type Engagement struct {
	QueueToken string
	EventID    string
	TicketNo   int64
	AccountID  string // logged-in only
	DeviceHash string // HMAC(DEVICE_PEPPER, device id), when a device id was sent
	JoinedAt   time.Time
}

// Engagement states reported on rejoin (api-contracts §3.3).
const (
	EngagementQueued = "QUEUED"
)

// Booking is the part of a bookings row join reads (empty-queue wait, rejoin).
type Booking struct {
	Status           string
	GrantToken       string
	BookingExpiresAt time.Time
}

// CaptchaResult is the outcome of one CAPTCHA verification.
type CaptchaResult int

// CAPTCHA outcomes. Unavailable means provider trouble: join fails open.
const (
	CaptchaValid CaptchaResult = iota
	CaptchaInvalid
	CaptchaUnavailable
)

func (r CaptchaResult) String() string {
	switch r {
	case CaptchaValid:
		return "valid"
	case CaptchaInvalid:
		return "invalid"
	default:
		return "unavailable"
	}
}

// Error codes (api-contracts Appendix A). QUEUE_NOT_READY, FULL_BOOKED and
// INTERNAL_ERROR are new (approved 2026-11-06, to be added to Appendix A).
const (
	CodeValidation       = "VALIDATION_ERROR"
	CodeNotFound         = "NOT_FOUND"
	CodeDeviceIDRequired = "DEVICE_ID_REQUIRED"
	CodeTokenExpired     = "TOKEN_EXPIRED"
	CodeTokenInvalid     = "TOKEN_INVALID"
	CodeTooManyRequests  = "TOO_MANY_REQUESTS"
	CodeCaptchaRequired  = "CAPTCHA_REQUIRED"
	CodeCaptchaFailed    = "CAPTCHA_FAILED"
	CodeSaleNotOpen      = "SALE_NOT_OPEN"
	CodeSaleClosed       = "SALE_CLOSED"
	CodeFullBooked       = "FULL_BOOKED"
	CodeQueueFull        = "QUEUE_FULL"
	CodeQueueNotReady    = "QUEUE_NOT_READY"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeDeviceMismatch   = "DEVICE_MISMATCH"
)

// Metric names.
const (
	MetricRateLimitFailOpen    = "RateLimitFailOpen"
	MetricCaptchaFailOpen      = "CaptchaFailOpenSeconds"
	MetricCaptchaMisconfigured = "CaptchaMisconfigured"
	MetricBR07Recovered        = "Br07JoinRecovered"
	MetricJoinRepaired         = "JoinRepaired"
	MetricDeviceMismatch       = "DeviceMismatch"       // a guest token presented from another device
	MetricTokenAccountMismatch = "TokenAccountMismatch" // an account's token presented by another account or a guest
)
