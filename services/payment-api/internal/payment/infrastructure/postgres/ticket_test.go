package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
)

func testBooking() entity.Booking {
	return entity.Booking{
		ID: "RB-00912", EventID: "EV-1", AccountID: "ACC-1",
		ContactEmail: "buyer@example", Status: entity.StatusConfirmed,
	}
}

var twoSeats = [][]any{
	{"bi-1", "EV-1", "cat-1", "Festival", int64(1500000), "hmac-1", "3171****33"},
	{"bi-2", "EV-1", "cat-1", "Festival", int64(1500000), "hmac-2", "3171****44"},
}

// ticketTx answers the reads issueTickets and the outbox helpers make.
func ticketTx(items [][]any, insertRow func(sql string) pgx.Row) *mockTx {
	tx := &mockTx{}
	tx.queryRowFn = func(_ context.Context, sql string, _ ...any) pgx.Row {
		switch {
		case strings.Contains(sql, "INSERT INTO tickets"):
			return insertRow(sql)
		case strings.Contains(sql, "FROM events"):
			return rowValues("Konser Rusuh", "2026-04-01", "GBK")
		}
		return rowErr(pgx.ErrNoRows)
	}
	tx.queryFn = func(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
		if strings.Contains(sql, "FROM booking_items") {
			return rowsOf(items...), nil
		}
		return &mockRows{}, nil
	}
	return tx
}

func inserted() func(string) pgx.Row {
	return func(string) pgx.Row { return rowValues("new-ticket") }
}

// One ticket per booking_item, one row per NIK (BR-02).
func TestIssueTickets_OnePerBookingItem(t *testing.T) {
	tx := ticketTx(twoSeats, inserted())

	got, err := newRepo(&mockDB{}).issueTickets(context.Background(), tx, testBooking())
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "bi-1", got[0].BookingItemID)
	assert.Equal(t, "bi-2", got[1].BookingItemID)
	assert.Equal(t, "hmac-1", got[0].NIKHmac)
	assert.Equal(t, int64(1500000), got[0].Price)
	assert.Equal(t, "RB-00912", got[0].BookingID)
}

// The double-QR guard. An outbox retry, or a settlement redelivered after the
// idempotency row was somehow cleared, must not mint a second ticket for the
// same seat - and the partial unique index on booking_item_id is what makes
// the guard possible.
func TestIssueTickets_IsIdempotentPerSeat(t *testing.T) {
	tx := ticketTx(twoSeats, inserted())

	_, err := newRepo(&mockDB{}).issueTickets(context.Background(), tx, testBooking())
	require.NoError(t, err)

	var insertSQL string
	for _, q := range tx.queries {
		if strings.Contains(q, "INSERT INTO tickets") {
			insertSQL = q
		}
	}
	require.NotEmpty(t, insertSQL)
	assert.Contains(t, insertSQL, "ON CONFLICT (booking_item_id)")
	assert.Contains(t, insertSQL, "DO NOTHING")
	// RETURNING is what makes a second call safe: only rows actually inserted
	// come back, so the caller queues exactly as many emails as tickets.
	assert.Contains(t, insertSQL, "RETURNING id")
}

// A seat whose ticket already exists returns no row. Not an error, and
// deliberately not re-emailed.
func TestIssueTickets_AlreadyIssuedSeatsAreSkipped(t *testing.T) {
	calls := 0
	tx := ticketTx(twoSeats, func(string) pgx.Row {
		calls++
		if calls == 1 {
			return rowErr(pgx.ErrNoRows) // first seat already had a ticket
		}
		return rowValues("new-ticket")
	})

	got, err := newRepo(&mockDB{}).issueTickets(context.Background(), tx, testBooking())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "bi-2", got[0].BookingItemID)
}

func TestIssueTickets_EverySeatAlreadyIssued_IssuesNothing(t *testing.T) {
	tx := ticketTx(twoSeats, func(string) pgx.Row { return rowErr(pgx.ErrNoRows) })

	got, err := newRepo(&mockDB{}).issueTickets(context.Background(), tx, testBooking())
	require.NoError(t, err)
	assert.Empty(t, got)
}

// The nonce source is injected rather than called inline, because it is the
// only thing standing between a screenshot and a forged ticket: a failure must
// stop the issue, not fall back to something weaker.
func TestIssueTickets_NonceFailure_StopsTheIssue(t *testing.T) {
	tx := ticketTx(twoSeats, inserted())
	repo := &BookingRepository{
		db:       &mockDB{},
		newID:    ids(),
		newNonce: func() (string, error) { return "", errors.New("entropy unavailable") },
	}

	_, err := repo.issueTickets(context.Background(), tx, testBooking())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "entropy")
	assert.Empty(t, tx.execMatching("INSERT INTO tickets"))
}

func TestIssueTickets_ItemReadFails_IsInternal(t *testing.T) {
	tx := &mockTx{queryFn: func(context.Context, string, ...any) (pgx.Rows, error) {
		return nil, errors.New("connection reset")
	}}

	_, err := newRepo(&mockDB{}).issueTickets(context.Background(), tx, testBooking())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "connection reset")
}

func TestIssueTickets_RowErrorAfterIteration_IsInternal(t *testing.T) {
	tx := &mockTx{queryFn: func(context.Context, string, ...any) (pgx.Rows, error) {
		rows := rowsOf(twoSeats...)
		rows.err = errors.New("unexpected EOF")
		return rows, nil
	}}

	_, err := newRepo(&mockDB{}).issueTickets(context.Background(), tx, testBooking())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "EOF")
}

// A guest purchase has no account to attribute the ticket to, so
// purchaser_account_id goes in as NULL rather than an empty string that would
// violate the foreign key.
func TestIssueTickets_GuestPurchase_HasNoPurchaser(t *testing.T) {
	var purchaserArg any = "sentinel"
	tx := ticketTx(twoSeats[:1], inserted())
	tx.queryRowFn = func(_ context.Context, sql string, args ...any) pgx.Row {
		if strings.Contains(sql, "INSERT INTO tickets") {
			purchaserArg = args[4]
			return rowValues("new-ticket")
		}
		if strings.Contains(sql, "FROM events") {
			return rowValues("Konser", "2026-04-01", "GBK")
		}
		return rowErr(pgx.ErrNoRows)
	}

	b := testBooking()
	b.AccountID = ""
	_, err := newRepo(&mockDB{}).issueTickets(context.Background(), tx, b)
	require.NoError(t, err)
	assert.Nil(t, purchaserArg)
}

// ── outbox ────────────────────────────────────────────────────────────────────

// One email per SEAT, all of them to the buyer. FR-26 wants every e-ticket
// delivered; ADR-006 §8.15 settled where to: booking_items never collected an
// address, so contact_email is the only one that exists and the buyer forwards
// each link on. Two seats still mean two emails - one link per seat, never one
// email carrying both.
func TestEnqueueTicketEmails_OnePerSeatAllToTheBuyer(t *testing.T) {
	tx := ticketTx(twoSeats, inserted())
	repo := newRepo(&mockDB{})

	tickets, err := repo.issueTickets(context.Background(), tx, testBooking())
	require.NoError(t, err)
	require.NoError(t, repo.enqueueTicketEmails(context.Background(), tx, testBooking(), tickets))

	rows := tx.execMatching("INSERT INTO outbox")
	require.Len(t, rows, 2)

	var payload events.TicketIssuePayload
	raw, ok := rows[0].args[3].([]byte)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal(raw, &payload))

	assert.Equal(t, string(events.TypeTicketIssue), rows[0].args[2])
	assert.Equal(t, "buyer@example", payload.ContactEmail)
	assert.Empty(t, payload.LegacyHolderEmail, "nothing new writes the dropped field")
	assert.Equal(t, "Konser Rusuh", payload.EventName)
	assert.Equal(t, "2026-04-01", payload.EventDate)
	assert.Equal(t, "GBK", payload.EventVenue)
	assert.Equal(t, "3171****33", payload.NIKMasked)
	// The QR carries the nonce, never the NIK: a QR is screenshotted,
	// forwarded and photographed, so it must carry no identity.
	assert.Equal(t, "nonce-x", payload.QRValue)
	assert.NotContains(t, string(raw), "hmac-1")

	var second events.TicketIssuePayload
	require.NoError(t, json.Unmarshal(rows[1].args[3].([]byte), &second))
	assert.Equal(t, "buyer@example", second.ContactEmail)
	assert.NotEqual(t, payload.NIKMasked, second.NIKMasked, "two distinct seats")
}

func TestEnqueueTicketEmails_NoTickets_QueuesNothing(t *testing.T) {
	tx := &mockTx{}
	require.NoError(t, newRepo(&mockDB{}).enqueueTicketEmails(context.Background(), tx, testBooking(), nil))
	assert.Empty(t, tx.execs)
}

// A missing catalogue row must not stop a paid customer getting a ticket: the
// email renders with blanks rather than not at all.
func TestEventFacts_MissingEvent_RendersBlank(t *testing.T) {
	tx := &mockTx{queryRowFn: func(context.Context, string, ...any) pgx.Row {
		return rowErr(pgx.ErrNoRows)
	}}

	f := newRepo(&mockDB{}).eventFacts(context.Background(), tx, "EV-missing")
	assert.Empty(t, f.Name)
	assert.Empty(t, f.Date)
	assert.Empty(t, f.Venue)
}

func TestEnqueueFailureNotice_CarriesThePaymentReference(t *testing.T) {
	tx := ticketTx(nil, inserted())

	require.NoError(t, newRepo(&mockDB{}).enqueueFailureNotice(
		context.Background(), tx, testBooking(), "RB-00912:1", 3000000, events.FailureDisputed))

	rows := tx.execMatching("INSERT INTO outbox")
	require.Len(t, rows, 1)
	assert.Equal(t, string(events.TypePaymentFailedNotice), rows[0].args[2])

	var payload events.PaymentFailedNoticePayload
	require.NoError(t, json.Unmarshal(rows[0].args[3].([]byte), &payload))
	assert.Equal(t, "RB-00912:1", payload.OrderID)
	assert.Equal(t, "buyer@example", payload.ContactEmail)
	assert.Equal(t, int64(3000000), payload.GrossAmount)
	assert.Equal(t, events.FailureDisputed, payload.Reason)
}

// FR-28 goes to the buyer, and to nobody else: §8.15 left contact_email as
// the only address a booking has. The notice must also not go looking for
// holder addresses, because the column is gone and the query would fail on a
// path that runs when a customer has just lost their seats.
func TestEnqueueExpiredNotice_ReachesTheBuyerAlone(t *testing.T) {
	tx := ticketTx(twoSeats, inserted())

	require.NoError(t, newRepo(&mockDB{}).enqueueExpiredNotice(context.Background(), tx, testBooking()))

	rows := tx.execMatching("INSERT INTO outbox")
	require.Len(t, rows, 1)
	assert.Equal(t, string(events.TypeBookingExpired), rows[0].args[2])

	raw, ok := rows[0].args[3].([]byte)
	require.True(t, ok)
	var payload events.BookingExpiredPayload
	require.NoError(t, json.Unmarshal(raw, &payload))
	assert.Equal(t, "buyer@example", payload.ContactEmail)
	assert.NotContains(t, string(raw), "holder_email")

	// No read of booking_items at all: one address needs no fan-out.
	for _, sql := range tx.queries {
		assert.NotContains(t, sql, "FROM booking_items")
	}
}

// The outbox write is in the caller's transaction, which is the whole point of
// the pattern: no instant exists at which a booking is CONFIRMED and its email
// is not queued, nor one at which the email is queued for a rolled-back
// booking.
func TestEnqueue_WritesInsideTheCallersTransaction(t *testing.T) {
	tx := &mockTx{}

	require.NoError(t, newRepo(&mockDB{}).enqueue(
		context.Background(), tx, "RB-00912", events.TypeTicketIssue, map[string]string{"a": "b"}))

	require.Len(t, tx.execs, 1)
	assert.Contains(t, tx.execs[0].sql, "INSERT INTO outbox")
	assert.Equal(t, "RB-00912", tx.execs[0].args[1])
	assert.Equal(t, 0, tx.commits, "enqueue must never commit on the caller's behalf")
}

func TestEnqueue_WriteFails_IsInternal(t *testing.T) {
	tx := &mockTx{}
	tx.execFn = func(context.Context, string, ...any) (pgconn.CommandTag, error) {
		return pgconn.CommandTag{}, errors.New("disk full")
	}

	err := newRepo(&mockDB{}).enqueue(
		context.Background(), tx, "RB-00912", events.TypeTicketIssue, map[string]string{"a": "b"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "disk full")
}
