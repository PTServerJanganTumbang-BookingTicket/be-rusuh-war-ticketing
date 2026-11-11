package usecase

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"sync"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/nikformat"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// maxParallelNIKChecks bounds the nik-verify invocations of one order: an order has at most three seats (BR-01).
const maxParallelNIKChecks = 3

// placeTimeout bounds the hold and the booking transaction. They run detached from the request: once the hold starts, a
// client that hangs up or a Lambda deadline must not leave its answer unknown (the script probably ran, and the
// customer's own NIKs would stay locked for the sale window). Together with the NIK checks (3 s) it stays inside the
// function's 10 s.
const placeTimeout = 5 * time.Second

// compensationTimeout bounds giving a hold back. It runs on a context detached from the request, so a client that
// hung up cannot leave seats held without a booking.
const compensationTimeout = 3 * time.Second

// BookingReader renders a booking for a grant; *BookingDetail is one.
type BookingReader interface {
	Get(ctx context.Context, g grantauth.Grant, bookingID string) (BookingView, error)
}

// CreateBookingDeps are the ports POST /bookings uses.
type CreateBookingDeps struct {
	Orders  outbound.Orders
	Holds   outbound.Holds
	Stock   outbound.Stock
	Queue   outbound.Queue
	NIKs    outbound.NIKVerifier
	Reader  BookingReader
	Metrics outbound.Metrics
	NewID   func() string // ids of booking_items
}

// SeatRequest is one entry of tickets[]: the category and exactly one of the three NIK sources.
type SeatRequest struct {
	CategoryID    string `json:"category_id"`
	NIK           string `json:"nik"`             // a typed NIK
	SavedNIKID    string `json:"saved_nik_id"`    // an entry of Daftar NIK Teman
	UseAccountNIK bool   `json:"use_account_nik"` // the logged-in customer's own registered NIK
}

// CreateRequest is the body of POST /bookings (api-contracts §4.3).
type CreateRequest struct {
	EventID      string        `json:"event_id"`
	Tickets      []SeatRequest `json:"tickets"`
	ContactEmail string        `json:"contact_email"`
}

// CreateResult is the booking and whether this call created it (201) or found it already held (200).
type CreateResult struct {
	View    BookingView
	Created bool
}

// CreateBooking serves POST /bookings (docs/05-booking/02-create-booking.md).
type CreateBooking struct {
	d        CreateBookingDeps
	maxSeats int
}

// NewCreateBooking builds the use case. maxSeats is MAX_TICKETS_PER_TRANSACTION (BR-01).
func NewCreateBooking(d CreateBookingDeps, maxSeats int) *CreateBooking {
	return &CreateBooking{d: d, maxSeats: maxSeats}
}

// Create runs the order in the order of the plan: status of the booking, body, categories, the NIK of every seat, then
// the hold and the booking transaction. The grant comes from pkg/grantauth; the booking's account is the grant's, never
// the request's.
func (c *CreateBooking) Create(ctx context.Context, g grantauth.Grant, req CreateRequest) (CreateResult, error) {
	return c.CreateFrom(ctx, g, func() (CreateRequest, error) { return req, nil })
}

// CreateFrom is Create with a body that is read only once the status allows it: a retry on a held booking answers 200
// whatever the body says, even one that is not JSON (contract §4.3), so the handler must not parse it first.
func (c *CreateBooking) CreateFrom(ctx context.Context, g grantauth.Grant, parse func() (CreateRequest, error)) (CreateResult, error) {
	// 1a. The status before the body: a retry on a held booking is 200 whatever the body says, and no Dukcapil call is
	// spent on a booking that is already held or over.
	st, found, err := c.d.Orders.Status(ctx, g.BookingID, g.Token)
	if err != nil {
		return CreateResult{}, unavailable("read booking status", err)
	}
	if !found {
		c.d.Metrics.Incr(domain.MetricLostGrant)
		return CreateResult{}, grantauth.ErrInvalid()
	}
	switch {
	case heldOrLater(st.Status):
		return c.existing(ctx, g)
	case st.Status.IsTerminal(): // LAPSED, EXPIRED, ABANDONED (CONFIRMED was handled above)
		return CreateResult{}, grantauth.ErrInvalid()
	case st.PastDeadline:
		return CreateResult{}, expiredErr()
	case st.Status == bookingstate.Granted:
		return CreateResult{}, notOpenedErr()
	}

	// 2. The body: nothing is resolved, checked or held for a body that is already wrong.
	req, err := parse()
	if err != nil {
		return CreateResult{}, apperror.ErrBadRequest("Body permintaan tidak valid").WithCode(apperror.CodeValidationError)
	}
	if err := c.validate(g, req); err != nil {
		return CreateResult{}, err
	}

	// 3. Categories: price and name as of now.
	cats, err := c.d.Orders.Categories(ctx, g.EventID)
	if err != nil {
		return CreateResult{}, unavailable("read categories", err)
	}
	byID := make(map[string]domain.Category, len(cats))
	for _, cat := range cats {
		byID[cat.ID] = cat
	}
	for i, s := range req.Tickets {
		if _, ok := byID[s.CategoryID]; !ok {
			return CreateResult{}, seatErr(i, "Kategori tiket tidak dikenal")
		}
	}

	// 4. The NIK of every seat: stored proof first, nik-verify for the typed ones.
	identities, err := c.resolve(ctx, g, req)
	if err != nil {
		return CreateResult{}, err
	}

	seats := make([]domain.Seat, len(req.Tickets))
	order := domain.HoldOrder{NIKs: make([]string, len(req.Tickets))}
	qty := map[string]int{} // category -> position in order.Categories
	for i, s := range req.Tickets {
		cat := byID[s.CategoryID]
		id := identities[i]
		seats[i] = domain.Seat{ID: c.d.NewID(), CategoryID: cat.ID, CategoryName: cat.Name, UnitPrice: cat.Price, NIKHmac: id.NIKHmac, NIKMasked: id.NIKMasked}
		order.NIKs[i] = id.NIKHmac
		if pos, ok := qty[cat.ID]; ok {
			order.Categories[pos].Qty++
		} else {
			qty[cat.ID] = len(order.Categories)
			order.Categories = append(order.Categories, domain.CategoryQty{CategoryID: cat.ID, Qty: 1})
		}
	}

	// 5-9. The hold (Redis) inside the booking transaction (Aurora). A customer who already left gets nothing held for
	// them; from here on the request cannot cancel the work.
	if err := ctx.Err(); err != nil {
		return CreateResult{}, unavailable("request cancelled before the hold", err)
	}
	ctx, cancelPlace := context.WithTimeout(context.WithoutCancel(ctx), placeTimeout)
	defer cancelPlace()
	taken := false
	hold := func(ctx context.Context) (domain.HoldAnswer, error) {
		ans, err := c.d.Holds.Hold(ctx, g.EventID, order)
		if err != nil {
			c.d.Metrics.Incr(domain.MetricHoldUnknown)
			return domain.HoldAnswer{}, err
		}
		taken = ans.Taken()
		return ans, nil
	}
	res, err := c.d.Orders.Place(ctx, domain.Placement{
		BookingID: g.BookingID, GrantToken: g.Token, EventID: g.EventID, ContactEmail: req.ContactEmail, Seats: seats,
	}, hold)

	// Redis is not in the transaction: a hold that was taken but did not end in a placed booking is given back.
	if taken && (err != nil || res.Outcome != domain.Placed) {
		if errors.Is(err, outbound.ErrCommitUnknown) {
			// The booking may exist. Releasing its stock would oversell; keeping it only under-sells until reconciliation.
			c.d.Metrics.Incr(domain.MetricHoldCommitUnknown)
		} else {
			c.compensate(ctx, g.EventID, order)
		}
	}
	if err != nil {
		return CreateResult{}, unavailable("place booking", err)
	}

	switch res.Outcome {
	case domain.Placed:
		return c.render(ctx, g, true)
	case domain.AlreadyPlaced:
		return c.existing(ctx, g)
	case domain.Gone:
		return CreateResult{}, grantauth.ErrInvalid()
	case domain.NotOpened:
		return CreateResult{}, notOpenedErr()
	case domain.TimeOver:
		return CreateResult{}, expiredErr()
	case domain.ClaimConflict:
		c.d.Metrics.Incr(domain.MetricNIKClaimConflict)
		return CreateResult{}, seatErr409(res.ConflictIndex)
	case domain.Refused:
		return CreateResult{}, c.refusal(ctx, g.EventID, res.Hold, order, cats)
	}
	return CreateResult{}, unavailable("place booking", errors.New("unknown outcome"))
}

// existing renders the booking of a retry: nothing was written.
func (c *CreateBooking) existing(ctx context.Context, g grantauth.Grant) (CreateResult, error) {
	return c.render(ctx, g, false)
}

// render reads the booking the way GET /bookings/{id} does, so 201 and GET share one shape.
func (c *CreateBooking) render(ctx context.Context, g grantauth.Grant, created bool) (CreateResult, error) {
	v, err := c.d.Reader.Get(ctx, g, g.BookingID)
	if err != nil {
		// A committed booking keeps its hold: the client retries and gets 200.
		return CreateResult{}, err
	}
	return CreateResult{View: v, Created: created}, nil
}

// compensate gives a hold back. It runs detached from the request context (a hung-up client must not leave seats held).
func (c *CreateBooking) compensate(ctx context.Context, eventID string, o domain.HoldOrder) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), compensationTimeout)
	defer cancel()
	missing, err := c.d.Holds.Release(rctx, eventID, o)
	if err != nil {
		// Stock and NIKs stay held without a booking until stock reconciliation (B5) repairs them.
		slog.Error("booking: cannot give a hold back", slog.String("event", eventID), slog.Any("err", err))
		c.d.Metrics.Incr(domain.MetricHoldCompensationFailed)
		return
	}
	c.d.Metrics.Incr(domain.MetricHoldCompensated)
	for i := 0; i < missing; i++ {
		c.d.Metrics.Incr(domain.MetricReleaseStockMissing)
	}
}

// refusal answers a refused hold. The booking stays SELECTING with the rest of its time; nothing was taken.
func (c *CreateBooking) refusal(ctx context.Context, eventID string, a domain.HoldAnswer, o domain.HoldOrder, cats []domain.Category) error {
	switch a.Code {
	case domain.HoldNoStock:
		return c.soldOut(ctx, eventID, a.Index, o, cats)
	case domain.HoldNIKTaken:
		return seatErr409(a.Index)
	case domain.HoldStockUninitialized:
		c.d.Metrics.Incr(domain.MetricStockUninitialized)
		return stockUnknownErr(domain.CodeStockUninitialized)
	case domain.HoldStockNegative:
		c.d.Metrics.Incr(domain.MetricStockNegative)
		slog.Error("booking: the hold guard fired: stock would go below zero")
		return stockUnknownErr(domain.CodeStockNegative)
	}
	// The script's own guards (QTY_*, NIK_COUNT_MISMATCH) cannot fire for an order that passed validation.
	slog.Error("booking: unexpected hold answer", slog.String("code", a.Code))
	return unavailable("hold", errors.New("unexpected hold answer "+a.Code))
}

// soldOut tells partly from totally sold out. Stock is the truth for "sold out"; the queue state only labels it.
func (c *CreateBooking) soldOut(ctx context.Context, eventID string, index int, o domain.HoldOrder, cats []domain.Category) error {
	ids := make([]string, len(cats))
	for i, cat := range cats {
		ids[i] = cat.ID
	}
	levels, missing, err := c.d.Stock.Levels(ctx, eventID, ids)
	if err != nil {
		return unavailable("read stock", err)
	}
	if len(missing) > 0 {
		// Unknown is not sold out (ADR-005 §8).
		c.d.Metrics.Incr(domain.MetricStockUninitialized)
		return stockUnknownErr(domain.CodeStockUninitialized)
	}
	totally := true
	for _, id := range ids {
		if levels[id] > 0 {
			totally = false
			break
		}
	}
	if !totally {
		if index < 0 || index >= len(o.Categories) {
			return unavailable("hold", errors.New("no stock answer without a category"))
		}
		short := o.Categories[index]
		return apperror.ErrConflict("Stok kategori ini tidak cukup").WithCode(domain.CodeInsufficientStock).WithData(map[string]any{
			"category_id": short.CategoryID, "available": max(levels[short.CategoryID], 0), "requested": short.Qty,
		})
	}
	state, at := "FULL_BOOKED", any(nil)
	snap, err := c.d.Queue.Snapshot(ctx, eventID)
	if err != nil {
		slog.Warn("booking: cannot read the queue state for the sold-out answer", slog.Any("err", err))
	} else {
		// The queue flips on its next tick (500 ms), so right after the last seat it still says OPEN: anything but
		// CLOSED is FULL_BOOKED.
		if snap.State == "CLOSED" {
			state = "CLOSED"
		}
		if !snap.FullBookedAt.IsZero() {
			at = snap.FullBookedAt.UTC().Format(time.RFC3339)
		}
	}
	return apperror.ErrConflict("Tiket habis untuk sementara").WithCode(domain.CodeTicketsUnavailable).
		WithData(map[string]any{"queue_state": state, "full_booked_at": at})
}

// validate checks the body without touching any store.
func (c *CreateBooking) validate(g grantauth.Grant, req CreateRequest) error {
	if req.EventID == "" {
		return apperror.ErrBadRequest("event_id wajib diisi").WithCode(apperror.CodeValidationError)
	}
	if req.EventID != g.EventID {
		return grantauth.ErrInvalid() // another event's page: the grant is not valid here
	}
	if len(req.Tickets) == 0 {
		return apperror.ErrBadRequest("Pilih minimal satu tiket").WithCode(apperror.CodeValidationError)
	}
	if len(req.Tickets) > c.maxSeats {
		return apperror.ErrBadRequest("Maksimal tiket per transaksi terlampaui").WithCode(domain.CodeQtyExceeded)
	}
	usedAccount := false
	for i, s := range req.Tickets {
		if s.CategoryID == "" {
			return seatErr(i, "Kategori tiket wajib diisi")
		}
		sources := 0
		if s.NIK != "" {
			sources++
		}
		if s.SavedNIKID != "" {
			sources++
		}
		if s.UseAccountNIK {
			sources++
		}
		if sources != 1 {
			return seatErr(i, "Setiap tiket harus punya tepat satu sumber NIK")
		}
		if (s.SavedNIKID != "" || s.UseAccountNIK) && g.AccountID == "" {
			return seatErr(i, "NIK tersimpan hanya untuk pembeli yang login")
		}
		if s.UseAccountNIK {
			if usedAccount {
				return seatErr(i, "NIK akun hanya boleh dipakai pada satu tiket")
			}
			usedAccount = true
		}
		if s.NIK != "" && !nikformat.IsValidFormat(nikformat.Normalize(s.NIK)) {
			return seatErr(i, "NIK harus 16 digit")
		}
	}
	if a, err := mail.ParseAddress(req.ContactEmail); err != nil || a.Address != req.ContactEmail {
		return apperror.ErrBadRequest("Email kontak tidak valid").WithCode(apperror.CodeValidationError)
	}
	return nil
}

// resolve returns the verified identity of every seat, in seat order. Seats that use the account's own NIK or a saved
// NIK come from stored proof (one read); typed NIKs go to nik-verify, in parallel. Duplicates are found last, by hash.
func (c *CreateBooking) resolve(ctx context.Context, g grantauth.Grant, req CreateRequest) ([]domain.Identity, error) {
	out := make([]domain.Identity, len(req.Tickets))

	var saved []string
	needsStored := false
	for _, s := range req.Tickets {
		if s.SavedNIKID != "" {
			saved = append(saved, s.SavedNIKID)
		}
		if s.SavedNIKID != "" || s.UseAccountNIK {
			needsStored = true
		}
	}
	if needsStored {
		ids, err := c.d.Orders.Identities(ctx, g.AccountID, saved)
		if err != nil {
			return nil, unavailable("read stored NIKs", err)
		}
		for i, s := range req.Tickets {
			switch {
			case s.UseAccountNIK:
				if ids.Own == nil {
					return nil, seatErr(i, "Akun belum punya NIK terverifikasi")
				}
				out[i] = *ids.Own
			case s.SavedNIKID != "":
				id, ok := ids.Saved[s.SavedNIKID]
				if !ok {
					// Unknown, another account's and deleted look the same: no id of another account leaks.
					return nil, seatErr(i, "NIK tersimpan tidak ditemukan")
				}
				out[i] = id
			}
		}
	}

	type answer struct {
		check domain.NIKCheck
		err   error
	}
	answers := make([]answer, len(req.Tickets))
	sem := make(chan struct{}, maxParallelNIKChecks)
	var wg sync.WaitGroup
	for i, s := range req.Tickets {
		if s.NIK == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			answers[i].check, answers[i].err = c.d.NIKs.Validate(ctx, s.NIK)
		}()
	}
	wg.Wait()
	for i, s := range req.Tickets {
		if s.NIK == "" {
			continue
		}
		a := answers[i]
		if a.err != nil {
			if !errors.Is(a.err, domain.ErrNIKUnavailable) {
				slog.Error("booking: unexpected NIK check error", slog.Int("seat", i), slog.Any("err", a.err))
			}
			return nil, apperror.ErrServiceUnavailable("Verifikasi NIK belum dapat dilakukan, coba lagi").WithCode(apperror.CodeDukcapilUnavailable)
		}
		if !a.check.Valid {
			code := apperror.CodeNIKNotFound
			if a.check.Status == "INACTIVE" {
				code = apperror.CodeNIKInactive
			}
			return nil, apperror.New(http.StatusBadRequest, "40000", "NIK tidak valid").WithCode(code).WithData(map[string]any{"index": i})
		}
		out[i] = domain.Identity{NIKHmac: a.check.NIKHmac, NIKMasked: a.check.NIKMasked}
	}

	first := make(map[string]int, len(out))
	for i, id := range out {
		if _, dup := first[id.NIKHmac]; dup {
			return nil, seatErr(i, "NIK yang sama dipakai pada dua tiket")
		}
		first[id.NIKHmac] = i
	}
	return out, nil
}

func heldOrLater(s bookingstate.Status) bool {
	switch s {
	case bookingstate.Held, bookingstate.AwaitingPayment, bookingstate.PendingCancellation, bookingstate.Confirmed:
		return true
	}
	return false
}

func expiredErr() error {
	return apperror.ErrConflict("Waktu memilih tiket sudah habis").WithCode(domain.CodeBookingExpired)
}

func notOpenedErr() error {
	return apperror.ErrConflict("Buka halaman tiket terlebih dulu").WithCode(domain.CodeBookingStatusInvalid)
}

func stockUnknownErr(code string) error {
	return apperror.ErrServiceUnavailable("Stok belum tersedia, coba lagi").WithCode(code)
}

// seatErr is a 400 VALIDATION_ERROR about one seat. The message never contains a NIK, only the index.
func seatErr(index int, msg string) error {
	return apperror.ErrBadRequest(msg).WithCode(apperror.CodeValidationError).WithData(map[string]any{"index": index})
}

// seatErr409 is the 409 NIK_ALREADY_USED about one seat.
func seatErr409(index int) error {
	return apperror.ErrConflict("NIK sudah dipakai untuk event ini").WithCode(domain.CodeNIKAlreadyUsed).WithData(map[string]any{"index": index})
}
