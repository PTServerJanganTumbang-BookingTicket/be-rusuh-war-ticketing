package postgres

import (
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

const codeUniqueViolation = "23505"

// BookingRepository implements the payment write paths.
//
// Every exported method is exactly one transaction. The use case never sees a
// pgx.Tx: a port that hands one out makes every caller a database client and
// makes "who commits this" unanswerable.
type BookingRepository struct {
	db DBConn
	// newID mints primary keys for payment_events and tickets.
	newID func() string
	// newNonce mints tickets.qr_nonce. Injected rather than called inline so
	// the randomness source is explicit and testable: the nonce is the only
	// thing standing between a screenshot and a forged ticket, so it must not
	// quietly become math/rand.
	newNonce func() (string, error)
}

// NewBookingRepository wires the repository.
func NewBookingRepository(db DBConn, newID func() string, newNonce func() (string, error)) portout.IBookingRepository {
	return &BookingRepository{db: db, newID: newID, newNonce: newNonce}
}

// Compile-time proof the repository satisfies the sweep port.
var _ portout.ISweepRepository = (*BookingRepository)(nil)

// isUniqueViolation reports a 23505, which on payment_events means the
// gateway_event_id was already stored — a redelivery.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == codeUniqueViolation
}

// wrapDB turns a driver error into something the HTTP layer can render without
// leaking SQL state to the client. The original error is logged so that 500s
// can be diagnosed from CloudWatch without exposing database internals in the
// response.
func wrapDB(err error, what string) error {
	if err == nil {
		return nil
	}
	slog.Error("database error",
		slog.String("operation", what),
		slog.Any("err", err))
	return apperror.ErrInternal(what)
}
