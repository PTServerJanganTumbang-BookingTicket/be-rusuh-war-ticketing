package usecase

import (
	"errors"
	"time"

	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/inbound"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// Config is the tunable half of the use case.
type Config struct {
	// IconBase prefixes the method and bank icon URLs in §5.1.
	IconBase string
	// PaymentTTL is the payment window. Sent to Midtrans as custom_expiry so
	// the gateway's window and ours end on the same second (BR-05).
	PaymentTTL time.Duration
	// MinRetryRemaining is the floor below which §5.3 refuses to switch method.
	MinRetryRemaining time.Duration
	// MaxPaymentAttempts caps instruments issued per booking; each attempt
	// mints a new VA or QR at Midtrans.
	MaxPaymentAttempts int
	// SettledTombstoneTTL is how long settled:{uuid} answers §3.3 after the
	// queue credential is deleted.
	SettledTombstoneTTL time.Duration
	// ServerKey signs and verifies Midtrans notifications.
	ServerKey string
}

// PaymentUsecase implements the five endpoints of contract §5.
type PaymentUsecase struct {
	bookings    portout.IBookingRepository
	gateway     portout.IMidtransClient
	breaker     portout.ICircuitBreaker
	credentials portout.ICredentialStore
	cfg         Config

	// now is injectable so the TTL arithmetic is testable without sleeping.
	now func() time.Time
}

// errBreakerOpen is returned by the charge closure when the gateway breaker is
// open. It travels back out through the repository, which rolls back, and the
// use case maps it to 503. It is deliberately not checked before the
// transaction opens: a replay of §5.2 calls no gateway at all, and must keep
// working while the breaker is open.
var errBreakerOpen = errors.New("payment: gateway breaker open")

// New wires the use case and returns the inbound port.
func New(
	bookings portout.IBookingRepository,
	gateway portout.IMidtransClient,
	breaker portout.ICircuitBreaker,
	credentials portout.ICredentialStore,
	cfg Config,
) portin.IPaymentUsecase {
	return &PaymentUsecase{
		bookings:    bookings,
		gateway:     gateway,
		breaker:     breaker,
		credentials: credentials,
		cfg:         cfg,
		now:         func() time.Time { return time.Now().UTC() },
	}
}
