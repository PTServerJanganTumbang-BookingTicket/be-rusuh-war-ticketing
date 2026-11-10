package usecase

import (
	"context"
	"log/slog"
	"net/url"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/eticket"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// BookingDetailDeps are the ports GET /bookings/{booking_id} uses.
type BookingDetailDeps struct {
	Details outbound.Details
	Links   outbound.TicketLinks
	Metrics outbound.Metrics
	Clock   func() time.Time
}

// BookingView is data of the 200 response (api-contracts §4.4).
type BookingView struct {
	BookingID     string       `json:"booking_id"`
	Status        string       `json:"status"`
	Event         EventView    `json:"event"`
	Tickets       []TicketView `json:"tickets"`
	Summary       SummaryView  `json:"summary"`
	PaymentTTL    *SessionView `json:"payment_ttl,omitempty"`    // PENDING_PAYMENT only
	PaymentMethod string       `json:"payment_method,omitempty"` // PAID only, as a label
}

// TicketView is one seat. ticket_id and eticket_url appear once the ticket is issued.
type TicketView struct {
	TicketID   string `json:"ticket_id,omitempty"`
	Category   string `json:"category"`
	Price      int64  `json:"price"`
	NIKMasked  string `json:"nik_masked"`
	ETicketURL string `json:"eticket_url,omitempty"`
}

// SummaryView is data.summary. Prices per seat are in tickets[].price.
type SummaryView struct {
	Quantity int   `json:"quantity"`
	Total    int64 `json:"total"`
}

// BookingDetail serves GET /bookings/{booking_id} (docs/05-booking/03-get-booking-detail.md).
type BookingDetail struct{ d BookingDetailDeps }

// NewBookingDetail builds the use case.
func NewBookingDetail(d BookingDetailDeps) *BookingDetail { return &BookingDetail{d: d} }

// paymentLabels turns bookings.payment_method into what the Success page shows.
var paymentLabels = map[string]string{"bank_transfer": "Virtual Account", "gopay": "GoPay"}

// Get renders the booking of the grant. Ownership first (ADR-005 §12): a path id that is not the grant's booking,
// or a booking bound to another grant, answers 404 like an unknown id, before any status is looked at.
func (u *BookingDetail) Get(ctx context.Context, g grantauth.Grant, bookingID string) (BookingView, error) {
	notFound := apperror.ErrNotFound("Booking tidak ditemukan").WithCode(domain.CodeNotFound)
	if bookingID != g.BookingID {
		return BookingView{}, notFound
	}
	d, found, err := u.d.Details.Detail(ctx, bookingID, g.Token)
	if err != nil {
		return BookingView{}, unavailable("read booking detail", err)
	}
	if !found {
		return BookingView{}, notFound
	}

	status := d.Status.DetailStatus()
	v := BookingView{
		BookingID: d.ID,
		Status:    status,
		Event:     eventView(d.Event),
		Tickets:   make([]TicketView, 0, len(d.Items)),
		Summary:   SummaryView{Quantity: len(d.Items)},
	}
	var linkExpiry time.Time
	if status == bookingstate.DetailPaid {
		if linkExpiry, err = eticket.ExpiresAt(d.Event.Date); err != nil {
			return BookingView{}, unavailable("e-ticket expiry", err)
		}
		v.PaymentMethod = u.paymentLabel(d.PaymentMethod)
	}
	for _, it := range d.Items {
		t := TicketView{Category: it.CategoryName, Price: it.UnitPrice, NIKMasked: it.NIKMasked}
		if status == bookingstate.DetailPaid && it.TicketID != "" {
			t.TicketID = it.TicketID
			if linkable[it.TicketStatus] {
				tok, err := u.d.Links.Mint(it.TicketID, linkExpiry)
				if err != nil {
					return BookingView{}, unavailable("mint e-ticket token", err)
				}
				t.ETicketURL = "/tickets/" + url.PathEscape(it.TicketID) + "/eticket?t=" + url.QueryEscape(tok)
			}
		}
		v.Tickets = append(v.Tickets, t)
		v.Summary.Total += it.UnitPrice
	}
	if status == bookingstate.DetailPendingPayment {
		if d.PaymentExpiresAt.IsZero() {
			// Written in the same transaction as HELD; a missing one is a data bug. No countdown beats a false one.
			u.d.Metrics.Incr(domain.MetricPaymentDeadlineMissing)
			slog.Error("booking detail: pending payment without a deadline", slog.String("booking", d.ID))
		} else {
			ttl := session(d.PaymentExpiresAt, u.d.Clock().UTC())
			v.PaymentTTL = &ttl
		}
	}
	return v, nil
}

// linkable are the ticket statuses that get an e-ticket link: a revoked or expired ticket keeps its id, not a link.
var linkable = map[string]bool{"VALID": true, "USED": true}

// paymentLabel turns the stored method into the Success page label. An unknown method (say a QRIS app's "qris") is
// shown as stored and counted, so the label map can be extended.
func (u *BookingDetail) paymentLabel(method string) string {
	if method == "" {
		return ""
	}
	if label, ok := paymentLabels[method]; ok {
		return label
	}
	u.d.Metrics.Incr(domain.MetricUnknownPaymentMethod)
	slog.Warn("booking detail: payment method without a label", slog.String("method", method))
	return method
}
