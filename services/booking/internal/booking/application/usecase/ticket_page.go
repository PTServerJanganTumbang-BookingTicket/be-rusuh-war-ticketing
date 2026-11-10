// Package usecase holds the booking use cases.
package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// TicketPageDeps are the ports GET /events/{event_id}/tickets uses.
type TicketPageDeps struct {
	Bookings outbound.Bookings
	Catalog  outbound.Catalog
	Stock    outbound.Stock
	Metrics  outbound.Metrics
	Clock    func() time.Time
}

// TicketPageView is data of the 200 response (api-contracts §4.1).
type TicketPageView struct {
	Event      EventView      `json:"event"`
	Session    SessionView    `json:"session"`
	Categories []CategoryView `json:"categories"`
	Rules      RulesView      `json:"rules"`
}

// EventView is data.event.
type EventView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Date     string `json:"date"`
	Venue    string `json:"venue"`
	ImageURL string `json:"image_url"`
}

// SessionView is data.session (and data.payment_ttl): the deadline and the server's clock. The client counts down
// from ExpiresAt with its own clock corrected by ServerTime (BR-10); there is no seconds-left field (contract v2.15).
type SessionView struct {
	ExpiresAt  string `json:"expires_at"`
	ServerTime string `json:"server_time"`
}

// CategoryView is one entry of data.categories.
type CategoryView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int64  `json:"price"`
	Available   int64  `json:"available"`
	MaxPerOrder int64  `json:"max_per_order"`
}

// RulesView is data.rules.
type RulesView struct {
	MaxTicketsPerOrder int  `json:"max_tickets_per_order"`
	OneNIKPerTicket    bool `json:"one_nik_per_ticket"`
}

// TicketPage serves GET /events/{event_id}/tickets (docs/05-booking/01-get-ticket-selection-page.md).
type TicketPage struct {
	d           TicketPageDeps
	maxPerOrder int
}

// NewTicketPage builds the use case. maxPerOrder is MAX_TICKETS_PER_TRANSACTION (BR-01).
func NewTicketPage(d TicketPageDeps, maxPerOrder int) *TicketPage {
	return &TicketPage{d: d, maxPerOrder: maxPerOrder}
}

// Get claims the booking (GRANTED → SELECTING, once) and renders the page. The grant comes from pkg/grantauth;
// the booking row decides whether it may still be used (the hash can outlive the booking).
func (p *TicketPage) Get(ctx context.Context, g grantauth.Grant, eventID string) (TicketPageView, error) {
	if g.EventID != eventID {
		return TicketPageView{}, grantauth.ErrInvalid()
	}
	b, found, err := p.d.Bookings.ClaimForSelection(ctx, g.BookingID, g.Token)
	if err != nil {
		return TicketPageView{}, unavailable("claim booking", err)
	}
	if !found {
		p.d.Metrics.Incr(domain.MetricLostGrant)
		slog.Error("ticket page: live grant without a booking row", slog.String("booking", g.BookingID))
		return TicketPageView{}, grantauth.ErrInvalid()
	}
	if b.EventID != eventID || b.Status.IsTerminal() {
		return TicketPageView{}, grantauth.ErrInvalid()
	}

	ev, cats, found, err := p.d.Catalog.Page(ctx, eventID)
	if err != nil {
		return TicketPageView{}, unavailable("read catalog", err)
	}
	if !found {
		return TicketPageView{}, apperror.ErrNotFound("Event tidak ditemukan").WithCode(domain.CodeNotFound)
	}
	ids := make([]string, len(cats))
	for i, c := range cats {
		ids[i] = c.ID
	}
	levels, missing, err := p.d.Stock.Levels(ctx, eventID, ids)
	if err != nil {
		return TicketPageView{}, unavailable("read stock", err)
	}
	if len(missing) > 0 {
		// Unknown is not sold out (ADR-005 §8): never show 0 for a key that is gone.
		p.d.Metrics.Incr(domain.MetricStockUninitialized)
		slog.Error("ticket page: stock key missing", slog.String("event", eventID), slog.Any("categories", missing))
		return TicketPageView{}, apperror.ErrServiceUnavailable("Stok belum tersedia, coba lagi").WithCode(domain.CodeStockUninitialized)
	}

	now := p.d.Clock().UTC()
	return TicketPageView{
		Event:      eventView(ev),
		Session:    session(currentDeadline(b), now),
		Categories: p.categories(cats, levels),
		Rules:      RulesView{MaxTicketsPerOrder: p.maxPerOrder, OneNIKPerTicket: true},
	}, nil
}

func (p *TicketPage) categories(cats []domain.Category, levels map[string]int64) []CategoryView {
	out := make([]CategoryView, len(cats))
	for i, c := range cats {
		available := max(levels[c.ID], 0)
		out[i] = CategoryView{
			ID: c.ID, Name: c.Name, Description: c.Description, Price: c.Price, Available: available,
			MaxPerOrder: min(int64(c.MaxPerOrder), available, int64(p.maxPerOrder)),
		}
	}
	return out
}

func eventView(e domain.Event) EventView {
	return EventView{ID: e.ID, Name: e.Name, Date: e.Date, Venue: e.Venue, ImageURL: e.ImageURL}
}

// currentDeadline is the deadline that applies now: the 5-minute booking deadline until the customer presses Pay,
// then the payment deadline (someone who pressed Back from Payment still holds seats).
func currentDeadline(b domain.Booking) time.Time {
	switch b.Status {
	case bookingstate.Held, bookingstate.AwaitingPayment, bookingstate.PendingCancellation:
		if !b.PaymentExpiresAt.IsZero() { // written in the same transaction as HELD; zero would be a data bug
			return b.PaymentExpiresAt
		}
	}
	return b.BookingExpiresAt
}

// session builds the deadline and clock pair. A deadline that has already passed (the sweep may run a second late)
// is sent as it is: the client shows the expired screen from it.
func session(deadline, now time.Time) SessionView {
	return SessionView{
		ExpiresAt:  deadline.UTC().Format(time.RFC3339),
		ServerTime: now.Format(time.RFC3339),
	}
}

// unavailable logs an infrastructure failure and answers 503 without its details.
func unavailable(what string, err error) error {
	slog.Error("booking: "+what, slog.Any("err", err))
	return apperror.ErrServiceUnavailable("Layanan sedang sibuk, coba lagi").WithCode(apinizer.ErrorCodeInternal)
}
