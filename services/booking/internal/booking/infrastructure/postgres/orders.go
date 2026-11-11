package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// statusSQL reads the status and whether the 5-minute deadline has passed, on Aurora's clock (the clock of the claim and
// of both sweeps, so two clocks never decide one case). Bound to id AND grant_token (ADR-005 §12).
const statusSQL = `SELECT status, booking_expires_at <= now() FROM bookings WHERE id = $1 AND grant_token = $2`

// lockSQL is the same read under a row lock. The SELECTING sweep's UPDATE waits for it, then sees HELD and skips the
// row; if the sweep got the row first, this read sees EXPIRED. The hold therefore never runs for a booking the sweep
// will end.
const lockSQL = statusSQL + ` FOR UPDATE`

const (
	// heldSQL moves SELECTING → HELD and starts the payment countdown: payment_expires_at = held_at + $2 seconds (BR-05).
	heldSQL = `UPDATE bookings SET status = 'HELD', held_at = now(), payment_expires_at = now() + make_interval(secs => $2::double precision),
	contact_email = $3 WHERE id = $1 AND status = 'SELECTING'`
	// itemSQL writes one seat. created_at is clock_timestamp(), not now(): now() is the transaction start, the same for
	// every seat, and GET /bookings/{id} lists the seats in the order they were chosen.
	itemSQL = `INSERT INTO booking_items (id, booking_id, event_id, category_id, category_name, unit_price, nik_hmac, nik_masked, created_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, clock_timestamp())`
	// claimSQL writes one NIK claim: one NIK, one seat per event, a database rule (BR-02).
	claimSQL = `INSERT INTO nik_claims (event_id, nik_hmac, booking_id) VALUES ($1, $2, $3)`
)

// nikClaimsKey is the primary key of nik_claims: a unique violation on it means a NIK already holds a seat.
const nikClaimsKey = "nik_claims_pkey"

var _ outbound.Orders = (*Store)(nil)

// Status reads the booking before the body is looked at: no lock, no transaction.
func (s *Store) Status(ctx context.Context, bookingID, grantToken string) (domain.BookingStatus, bool, error) {
	st, err := scanStatus(s.db.QueryRow(ctx, statusSQL, bookingID, grantToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BookingStatus{}, false, nil
	}
	if err != nil {
		return domain.BookingStatus{}, false, fmt.Errorf("read booking status: %w", err)
	}
	return st, true, nil
}

func scanStatus(row pgx.Row) (domain.BookingStatus, error) {
	var (
		raw  string
		past bool
	)
	if err := row.Scan(&raw, &past); err != nil {
		return domain.BookingStatus{}, err
	}
	st, err := bookingstate.Parse(raw)
	if err != nil {
		return domain.BookingStatus{}, err // an unknown status is an error, never a default
	}
	return domain.BookingStatus{Status: st, PastDeadline: past}, nil
}

// Categories reads the ticket categories of an event in display order.
func (s *Store) Categories(ctx context.Context, eventID string) ([]domain.Category, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, description, price, max_per_order
FROM ticket_categories WHERE event_id = $1 ORDER BY sort_order, id`, eventID)
	if err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}
	defer rows.Close()
	var cats []domain.Category
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Price, &c.MaxPerOrder); err != nil {
			return nil, fmt.Errorf("read category: %w", err)
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}
	return cats, nil
}

// Identities reads the account's own verified NIK and the saved NIKs among savedIDs in one round trip. Only this
// account's saved NIKs match, so an id of another account looks like an unknown one.
func (s *Store) Identities(ctx context.Context, accountID string, savedIDs []string) (domain.Identities, error) {
	batch := &pgx.Batch{}
	batch.Queue(`SELECT nik_hmac, nik_masked FROM accounts WHERE id = $1 AND nik_verified_at IS NOT NULL`, accountID)
	if len(savedIDs) > 0 {
		batch.Queue(`SELECT id, nik_hmac, nik_masked FROM saved_niks WHERE account_id = $1 AND id = ANY($2)`, accountID, savedIDs)
	}
	res := s.db.SendBatch(ctx, batch)
	defer func() { _ = res.Close() }()

	var out domain.Identities
	var own domain.Identity
	err := res.QueryRow().Scan(&own.NIKHmac, &own.NIKMasked)
	switch {
	case err == nil:
		out.Own = &own
	case !errors.Is(err, pgx.ErrNoRows):
		return domain.Identities{}, fmt.Errorf("read account NIK: %w", err)
	}
	if len(savedIDs) == 0 {
		return out, nil
	}
	rows, err := res.Query()
	if err != nil {
		return domain.Identities{}, fmt.Errorf("read saved NIKs: %w", err)
	}
	defer rows.Close()
	out.Saved = map[string]domain.Identity{}
	for rows.Next() {
		var id string
		var ident domain.Identity
		if err := rows.Scan(&id, &ident.NIKHmac, &ident.NIKMasked); err != nil {
			return domain.Identities{}, fmt.Errorf("read saved NIK: %w", err)
		}
		out.Saved[id] = ident
	}
	if err := rows.Err(); err != nil {
		return domain.Identities{}, fmt.Errorf("read saved NIKs: %w", err)
	}
	return out, nil
}

// Place is the booking transaction: lock the row, repeat the status checks under the lock, run the hold, then write
// HELD with its deadline, the seats and the NIK claims in one batch, and commit. The lock is held only for those few
// milliseconds: the NIK checks (an HTTP-sized wait) happen before, outside the transaction.
func (s *Store) Place(ctx context.Context, p domain.Placement, hold outbound.HoldFunc) (domain.PlaceResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return domain.PlaceResult{}, fmt.Errorf("begin: %w", err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = tx.Rollback(context.WithoutCancel(ctx)) // a cancelled request must not leave the row locked
		}
	}()

	st, err := scanStatus(tx.QueryRow(ctx, lockSQL, p.BookingID, p.GrantToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PlaceResult{Outcome: domain.Gone}, nil
	}
	if err != nil {
		return domain.PlaceResult{}, fmt.Errorf("lock booking: %w", err)
	}
	switch {
	case st.Status == bookingstate.Held, st.Status == bookingstate.AwaitingPayment,
		st.Status == bookingstate.PendingCancellation, st.Status == bookingstate.Confirmed:
		return domain.PlaceResult{Outcome: domain.AlreadyPlaced}, nil
	case st.Status.IsTerminal(): // LAPSED, EXPIRED, ABANDONED
		return domain.PlaceResult{Outcome: domain.Gone}, nil
	case st.PastDeadline:
		return domain.PlaceResult{Outcome: domain.TimeOver}, nil
	case st.Status == bookingstate.Granted:
		return domain.PlaceResult{Outcome: domain.NotOpened}, nil
	}

	ans, err := hold(ctx)
	if err != nil {
		return domain.PlaceResult{}, err
	}
	if !ans.Taken() {
		// Nothing was taken and nothing changed: commit as it is, the booking stays SELECTING with its time.
		finished = true
		if err := tx.Commit(ctx); err != nil {
			return domain.PlaceResult{}, fmt.Errorf("commit: %w", err)
		}
		return domain.PlaceResult{Outcome: domain.Refused, Hold: ans}, nil
	}

	batch := &pgx.Batch{}
	batch.Queue(heldSQL, p.BookingID, int64(domain.PaymentWindow/time.Second), p.ContactEmail)
	for _, seat := range p.Seats {
		batch.Queue(itemSQL, seat.ID, p.BookingID, p.EventID, seat.CategoryID, seat.CategoryName, seat.UnitPrice, seat.NIKHmac, seat.NIKMasked)
	}
	for _, seat := range p.Seats {
		batch.Queue(claimSQL, p.EventID, seat.NIKHmac, p.BookingID)
	}
	conflictAt, err := writeBatch(ctx, tx, batch, len(p.Seats))
	if err != nil {
		return domain.PlaceResult{}, err
	}
	if conflictAt >= 0 {
		return domain.PlaceResult{Outcome: domain.ClaimConflict, ConflictIndex: conflictAt}, nil
	}

	finished = true
	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) || pgconn.SafeToRetry(err) {
			// Known not to have committed (the server rolled the transaction back, or the commit never left the
			// client): an ordinary failure, so the hold is given back and the customer can retry.
			return domain.PlaceResult{}, fmt.Errorf("commit: %w", err)
		}
		// The transaction may or may not have committed: the caller must not release the hold (it would oversell).
		return domain.PlaceResult{}, errors.Join(fmt.Errorf("commit: %w", err), outbound.ErrCommitUnknown)
	}
	return domain.PlaceResult{Outcome: domain.Placed}, nil
}

// writeBatch sends the update, the seats and the claims, and reads every result in order. A unique violation on
// nik_claims at claim i is reported as conflictAt = i (Redis missed a NIK: a flush or failover); any other failure is an
// error. The batch is closed before it returns, as the transaction needs before it commits or rolls back.
func writeBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch, seats int) (int, error) {
	res := tx.SendBatch(ctx, batch)
	defer func() { _ = res.Close() }()

	tag, err := res.Exec()
	if err != nil {
		return -1, fmt.Errorf("hold booking: %w", err)
	}
	if tag.RowsAffected() != 1 {
		// The row is locked and was SELECTING a moment ago: nothing else can have changed it.
		return -1, fmt.Errorf("hold booking: updated %d rows, want 1", tag.RowsAffected())
	}
	for i := 0; i < seats; i++ {
		if _, err := res.Exec(); err != nil {
			return -1, fmt.Errorf("write seat %d: %w", i, err)
		}
	}
	for i := 0; i < seats; i++ {
		if _, err := res.Exec(); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == nikClaimsKey {
				return i, nil
			}
			return -1, fmt.Errorf("claim NIK of seat %d: %w", i, err)
		}
	}
	return -1, nil
}
