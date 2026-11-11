package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
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
	// SignatureSecretKey verifies the signature_key on every notification
	// (§5.5). NOT the server key, which authenticates charges and lives in the
	// gateway client: Midtrans split the two on 28 October 2026. This usecase
	// never charges, so the server key has no business being here at all.
	SignatureSecretKey string
}

// PaymentUsecase implements the five endpoints of contract §5.
type PaymentUsecase struct {
	bookings    portout.IBookingRepository
	gateway     portout.IMidtransClient
	breaker     portout.ICircuitBreaker
	credentials portout.ICredentialStore
	switches    portout.IMethodSwitches
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
	switches portout.IMethodSwitches,
	cfg Config,
) portin.IPaymentUsecase {
	return &PaymentUsecase{
		bookings:    bookings,
		gateway:     gateway,
		breaker:     breaker,
		credentials: credentials,
		switches:    switches,
		cfg:         cfg,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

// disabledMethods reads the operator kill switch, treating an unreadable
// switch store as "nothing is switched off".
//
// The adapter already does that and returns a nil error, so this is belt and
// braces for any other implementation of the port: the one thing a kill switch
// must never do is take payments down when its own storage is unavailable.
func (u *PaymentUsecase) disabledMethods(ctx context.Context) entity.Disabled {
	if u.switches == nil {
		return nil
	}
	off, err := u.switches.Disabled(ctx)
	if err != nil {
		slog.WarnContext(ctx, "method switches unreadable, offering every method",
			slog.Any("err", err))
		return nil
	}
	return off
}
