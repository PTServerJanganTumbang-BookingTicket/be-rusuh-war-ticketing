package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
)

// eventFacts is the small projection a ticket email needs.
//
// Copied onto the outbox payload rather than joined by the notification
// service: the email must render what applied at purchase, and notification
// has no business reaching into the events table.
type eventFacts struct {
	Name  string
	Date  string
	Venue string
}

func (r *BookingRepository) eventFacts(ctx context.Context, tx pgx.Tx, eventID string) eventFacts {
	var f eventFacts
	// Best effort: a missing catalogue row must not stop a paid customer from
	// getting a ticket. The email renders with blanks rather than not at all.
	_ = tx.QueryRow(ctx,
		`SELECT COALESCE(name, ''), COALESCE(to_char(event_date, 'YYYY-MM-DD'), ''), COALESCE(venue, '')
           FROM events WHERE id = $1`, eventID).Scan(&f.Name, &f.Date, &f.Venue)
	return f
}

// issueTickets writes one ticket per booking_item.
//
// ON CONFLICT (booking_item_id) DO NOTHING is the double-QR guard: an outbox
// retry, or a settlement notification redelivered after the idempotency row was
// somehow cleared, must not mint a second ticket for the same seat. The partial
// unique index added in 0006 is what makes that guard possible — 0002 had only
// booking_id, so two attempts for one booking did not collide.
//
// RETURNING is what makes this safe to call twice: only rows actually inserted
// come back, so the caller enqueues exactly as many emails as tickets created.
func (r *BookingRepository) issueTickets(ctx context.Context, tx pgx.Tx, b entity.Booking) ([]entity.Ticket, error) {
	// No holder_email: migration 0010 dropped the column. ADR-006 §8.15 sends
	// every e-ticket of a booking to bookings.contact_email, so the seat no
	// longer carries an address of its own. The NIK still binds the seat
	// (BR-02), which is what decides who may enter - the email never did.
	rows, err := tx.Query(ctx,
		`SELECT id, event_id, category_id, category_name, unit_price, nik_hmac, nik_masked
           FROM booking_items WHERE booking_id = $1 ORDER BY created_at, id`, b.ID)
	if err != nil {
		return nil, wrapDB(err, "could not read booking items")
	}
	var items []entity.BookingItem
	for rows.Next() {
		var it entity.BookingItem
		if err := rows.Scan(&it.ID, &it.EventID, &it.CategoryID, &it.CategoryName,
			&it.UnitPrice, &it.NIKHmac, &it.NIKMasked); err != nil {
			rows.Close()
			return nil, wrapDB(err, "could not read booking items")
		}
		it.BookingID = b.ID
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, wrapDB(err, "could not read booking items")
	}

	issued := make([]entity.Ticket, 0, len(items))
	for _, it := range items {
		nonce, err := r.newNonce()
		if err != nil {
			return nil, wrapDB(err, "could not mint ticket nonce")
		}
		var purchaser *string
		if b.AccountID != "" {
			purchaser = &b.AccountID
		}
		t := entity.Ticket{
			ID: r.newID(), BookingID: b.ID, BookingItemID: it.ID, EventID: it.EventID,
			// tickets.holder_email survives 0010 and is NOT NULL. §8.15 fills it
			// with contact_email: the buyer is the only address collected, and
			// they forward the e-ticket link themselves.
			NIKHmac: it.NIKHmac, NIKMasked: it.NIKMasked, HolderEmail: b.ContactEmail,
			CategoryID: it.CategoryID, CategoryName: it.CategoryName,
			Price: it.UnitPrice, QRNonce: nonce,
		}
		var gotID string
		err = tx.QueryRow(ctx,
			`INSERT INTO tickets
                (id, booking_id, booking_item_id, event_id, purchaser_account_id,
                 nik_hmac, nik_masked, holder_email, category_id, category_name,
                 price, qr_nonce, status)
              VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'VALID')
              ON CONFLICT (booking_item_id) WHERE booking_item_id IS NOT NULL
              DO NOTHING
              RETURNING id`,
			t.ID, t.BookingID, t.BookingItemID, t.EventID, purchaser,
			t.NIKHmac, t.NIKMasked, t.HolderEmail, t.CategoryID, t.CategoryName,
			t.Price, t.QRNonce).Scan(&gotID)
		if err == pgx.ErrNoRows {
			// Already issued for this seat. Not an error, and deliberately not
			// re-emailed.
			continue
		}
		if err != nil {
			return nil, wrapDB(err, "could not issue ticket")
		}
		issued = append(issued, t)
	}
	return issued, nil
}

// enqueue writes one outbox row inside the caller's transaction.
//
// Same transaction as the state change it describes, which is the whole point
// of the pattern: there is no instant at which a booking is CONFIRMED and its
// e-ticket email is not queued, and none at which the email is queued for a
// booking that rolled back.
func (r *BookingRepository) enqueue(ctx context.Context, tx pgx.Tx, aggregateID string, t events.Type, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return wrapDB(err, "could not encode event")
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO outbox (id, aggregate_id, type, payload) VALUES ($1, $2, $3, $4)`,
		r.newID(), aggregateID, string(t), body); err != nil {
		return wrapDB(err, "could not queue event")
	}
	return nil
}

// enqueueTicketEmails queues one e-ticket per issued seat, all to the buyer.
//
// One address per booking (ADR-006 §8.15, contract v2.14, FR-26 as amended):
// every e-ticket goes to bookings.contact_email and the buyer forwards the
// links. The earlier design collected an address per seat; it was dropped
// because the e-ticket link already covers handing a seat to someone else,
// and a per-seat email field is input and validation burden for nothing.
//
// Still one event per SEAT, not one per booking: each carries its own NIK and
// QR, and §6.2's link is per ticket.
func (r *BookingRepository) enqueueTicketEmails(ctx context.Context, tx pgx.Tx, b entity.Booking, tickets []entity.Ticket) error {
	if len(tickets) == 0 {
		return nil
	}
	f := r.eventFacts(ctx, tx, b.EventID)
	for _, t := range tickets {
		if err := r.enqueue(ctx, tx, b.ID, events.TypeTicketIssue, events.TicketIssuePayload{
			TicketID: t.ID, BookingID: b.ID, ContactEmail: b.ContactEmail,
			EventName: f.Name, EventDate: f.Date, EventVenue: f.Venue,
			Category: t.CategoryName, NIKMasked: t.NIKMasked,
			// The QR carries the nonce, never the NIK: a QR is screenshotted,
			// forwarded and photographed, so it must carry no identity.
			QRValue: t.QRNonce,
		}); err != nil {
			return err
		}
	}
	return nil
}

// enqueueFailureNotice queues the FR-28a notice.
func (r *BookingRepository) enqueueFailureNotice(ctx context.Context, tx pgx.Tx, b entity.Booking, orderID string, gross int64, reason events.FailureReason) error {
	f := r.eventFacts(ctx, tx, b.EventID)
	return r.enqueue(ctx, tx, b.ID, events.TypePaymentFailedNotice, events.PaymentFailedNoticePayload{
		BookingID: b.ID, OrderID: orderID, ContactEmail: b.ContactEmail,
		EventName: f.Name, GrossAmount: gross, Reason: reason,
	})
}

// enqueueExpiredNotice queues FR-28, to the buyer alone.
//
// ADR-006 §8.15: booking.expired and the payment-failure notice go only to
// contact_email. There is no other address to send to - the seats never
// carried one - and nothing is lost: the holder learns from whoever bought.
func (r *BookingRepository) enqueueExpiredNotice(ctx context.Context, tx pgx.Tx, b entity.Booking) error {
	f := r.eventFacts(ctx, tx, b.EventID)
	return r.enqueue(ctx, tx, b.ID, events.TypeBookingExpired, events.BookingExpiredPayload{
		BookingID: b.ID, EventID: b.EventID, ContactEmail: b.ContactEmail, EventName: f.Name,
	})
}
