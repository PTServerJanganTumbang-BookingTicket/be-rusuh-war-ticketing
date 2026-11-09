package usecase

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/eticket"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

type fakeDetails struct {
	detail domain.BookingDetail
	found  bool
	err    error
	calls  int
}

func (f *fakeDetails) Detail(context.Context, string, string) (domain.BookingDetail, bool, error) {
	f.calls++
	return f.detail, f.found, f.err
}

type detailRig struct {
	m      *fakeMetrics
	d      *fakeDetails
	signer *eticket.Signer
	uc     *BookingDetail
}

func newDetailRig(t *testing.T) *detailRig {
	t.Helper()
	signer, err := eticket.NewSigner([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	r := &detailRig{
		signer: signer,
		d: &fakeDetails{found: true, detail: domain.BookingDetail{
			ID: "RB-1", EventID: "evt_a", Status: bookingstate.Held, PaymentExpiresAt: now.Add(10 * time.Minute),
			Event: domain.Event{ID: "evt_a", Name: "Konser A", Date: "2026-12-25", Venue: "GBK", ImageURL: "https://cdn/a.jpg"},
			Items: []domain.Item{
				{ID: "it_1", CategoryName: "VIP", UnitPrice: 1500000, NIKMasked: "3171********0001"},
				{ID: "it_2", CategoryName: "Festival", UnitPrice: 850000, NIKMasked: "3171********0002"},
			},
		}},
	}
	r.m = &fakeMetrics{counts: map[string]int{}}
	r.uc = NewBookingDetail(BookingDetailDeps{Details: r.d, Links: signer, Metrics: r.m, Clock: func() time.Time { return now }})
	return r
}

var detailGrant = grant // booking RB-1 of event evt_a

func TestABookingOfAnotherGrantIsNotFoundLikeAnUnknownID(t *testing.T) {
	r := newDetailRig(t)
	r.d.found = false
	_, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
	wantAppError(t, err, http.StatusNotFound, domain.CodeNotFound)
}

func TestAPathIDOtherThanTheGrantsBookingIsNotFoundWithoutADatabaseRead(t *testing.T) {
	r := newDetailRig(t)
	_, err := r.uc.Get(context.Background(), detailGrant, "RB-2")
	wantAppError(t, err, http.StatusNotFound, domain.CodeNotFound)
	if r.d.calls != 0 {
		t.Fatalf("database read %d times, want 0", r.d.calls)
	}
}

func TestEachStoredStatusMapsToItsDetailStatus(t *testing.T) {
	for _, st := range bookingstate.All {
		t.Run(string(st), func(t *testing.T) {
			r := newDetailRig(t)
			r.d.detail.Status = st
			v, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
			if err != nil || v.Status != st.DetailStatus() {
				t.Fatalf("status %q (%v), want %q", v.Status, err, st.DetailStatus())
			}
		})
	}
}

func TestAHeldBookingShowsThePaymentCountdown(t *testing.T) {
	r := newDetailRig(t)
	v, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
	if err != nil || v.PaymentTTL == nil || v.PaymentTTL.ExpiresAt != "2026-11-10T19:12:00Z" || v.PaymentTTL.TTLSeconds != 600 {
		t.Fatalf("payment_ttl %+v (%v), want 600 s to 19:12", v.PaymentTTL, err)
	}
	r.d.detail.Status = bookingstate.PendingCancellation
	r.d.detail.PaymentExpiresAt = now.Add(-10 * time.Second) // grace running
	if v, err := r.uc.Get(context.Background(), detailGrant, "RB-1"); err != nil || v.PaymentTTL == nil || v.PaymentTTL.TTLSeconds != 0 {
		t.Fatalf("payment_ttl in the grace %+v (%v), want ttl 0", v.PaymentTTL, err)
	}
}

func TestOnlyAPendingPaymentHasACountdown(t *testing.T) {
	for _, st := range []bookingstate.Status{bookingstate.Selecting, bookingstate.Confirmed, bookingstate.Expired} {
		r := newDetailRig(t)
		r.d.detail.Status = st
		if v, err := r.uc.Get(context.Background(), detailGrant, "RB-1"); err != nil || v.PaymentTTL != nil {
			t.Errorf("%s: payment_ttl %+v (%v), want none", st, v.PaymentTTL, err)
		}
	}
}

func TestAPaidBookingLinksEachIssuedTicketAndNoneNotIssuedYet(t *testing.T) {
	r := newDetailRig(t)
	r.d.detail.Status = bookingstate.Confirmed
	r.d.detail.Items[0].TicketID, r.d.detail.Items[0].TicketStatus = "TKT-001", "VALID" // the second is not issued yet
	v, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
	if err != nil || v.Status != "PAID" {
		t.Fatalf("status %q (%v)", v.Status, err)
	}
	first, second := v.Tickets[0], v.Tickets[1]
	if first.TicketID != "TKT-001" || !strings.HasPrefix(first.ETicketURL, "/tickets/TKT-001/eticket?t=") {
		t.Fatalf("issued ticket = %+v", first)
	}
	tok := strings.TrimPrefix(first.ETicketURL, "/tickets/TKT-001/eticket?t=")
	exp, _ := eticket.ExpiresAt("2026-12-25")
	if err := r.signer.Verify(tok, "TKT-001", exp.Add(-time.Second)); err != nil {
		t.Fatalf("the link's token does not verify until the day after the event: %v", err)
	}
	if err := r.signer.Verify(tok, "TKT-001", exp); err == nil {
		t.Fatal("the link's token must stop at 00:00 Jakarta the day after the event")
	}
	if second.TicketID != "" || second.ETicketURL != "" {
		t.Fatalf("a seat without a ticket yet must have no id and no link, got %+v", second)
	}
}

func TestTheETicketTokenIsURLSafe(t *testing.T) {
	r := newDetailRig(t)
	r.d.detail.Status = bookingstate.Confirmed
	r.d.detail.Items[0].TicketID, r.d.detail.Items[0].TicketStatus = "TKT-001", "VALID"
	v, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimPrefix(v.Tickets[0].ETicketURL, "/tickets/TKT-001/eticket?t=")
	if strings.ContainsAny(tok, "+/=&?# ") {
		t.Fatalf("token %q needs escaping in a query string", tok)
	}
}

func TestThePaymentMethodLabelIsShownOnlyWhenPaid(t *testing.T) {
	cases := map[string]string{"bank_transfer": "Virtual Account", "gopay": "GoPay", "": "", "qris": "qris"}
	for stored, label := range cases {
		r := newDetailRig(t)
		r.d.detail.Status = bookingstate.Confirmed
		r.d.detail.PaymentMethod = stored
		if v, err := r.uc.Get(context.Background(), detailGrant, "RB-1"); err != nil || v.PaymentMethod != label {
			t.Errorf("paid with %q: label %q (%v), want %q", stored, v.PaymentMethod, err, label)
		}
	}
	r := newDetailRig(t)
	r.d.detail.Status = bookingstate.AwaitingPayment
	r.d.detail.PaymentMethod = "gopay"
	if v, err := r.uc.Get(context.Background(), detailGrant, "RB-1"); err != nil || v.PaymentMethod != "" {
		t.Fatalf("not paid yet: label %q (%v), want none", v.PaymentMethod, err)
	}
}

func TestTheSummaryIsTheCountAndTheSumWithNoPricePerTicket(t *testing.T) {
	r := newDetailRig(t)
	v, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
	if err != nil || v.Summary != (SummaryView{Quantity: 2, Total: 2350000}) {
		t.Fatalf("summary %+v (%v)", v.Summary, err)
	}
	b, _ := json.Marshal(v)
	for _, field := range []string{"price_per_ticket", "paid_at"} {
		if strings.Contains(string(b), field) {
			t.Errorf("response carries %s: %s", field, b)
		}
	}
}

func TestTheResponseCarriesTheEventAndEachSeat(t *testing.T) {
	r := newDetailRig(t)
	v, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
	if err != nil {
		t.Fatal(err)
	}
	if v.BookingID != "RB-1" || v.Event != (EventView{ID: "evt_a", Name: "Konser A", Date: "2026-12-25", Venue: "GBK", ImageURL: "https://cdn/a.jpg"}) {
		t.Fatalf("booking %q event %+v", v.BookingID, v.Event)
	}
	want := []TicketView{
		{Category: "VIP", Price: 1500000, NIKMasked: "3171********0001"},
		{Category: "Festival", Price: 850000, NIKMasked: "3171********0002"},
	}
	if !reflect.DeepEqual(v.Tickets, want) {
		t.Fatalf("tickets %+v", v.Tickets)
	}
}

func TestABookingWithoutSeatsHasAnEmptyTicketList(t *testing.T) {
	r := newDetailRig(t)
	r.d.detail.Status = bookingstate.Selecting
	r.d.detail.Items = nil
	v, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(v); !strings.Contains(string(b), `"tickets":[]`) {
		t.Fatalf("tickets must be [] (never null): %s", b)
	}
}

func TestADatabaseFailureOnTheDetailIsA503(t *testing.T) {
	r := newDetailRig(t)
	r.d.err = errDown
	_, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
}

func TestAnUnknownPaymentMethodIsShownAsStoredAndCounted(t *testing.T) {
	r := newDetailRig(t)
	r.d.detail.Status = bookingstate.Confirmed
	r.d.detail.PaymentMethod = "qris"
	if v, err := r.uc.Get(context.Background(), detailGrant, "RB-1"); err != nil || v.PaymentMethod != "qris" {
		t.Fatalf("label %q (%v)", v.PaymentMethod, err)
	}
	if r.m.counts[domain.MetricUnknownPaymentMethod] != 1 {
		t.Fatalf("metrics %v", r.m.counts)
	}
}

func TestARevokedOrExpiredTicketHasNoLink(t *testing.T) {
	for _, st := range []string{"REVOKED", "EXPIRED"} {
		r := newDetailRig(t)
		r.d.detail.Status = bookingstate.Confirmed
		r.d.detail.Items[0].TicketID, r.d.detail.Items[0].TicketStatus = "TKT-001", st
		v, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
		if err != nil || v.Tickets[0].ETicketURL != "" || v.Tickets[0].TicketID != "TKT-001" {
			t.Fatalf("%s ticket %+v (%v), want its id without a link", st, v.Tickets[0], err)
		}
	}
}

func TestAPendingPaymentWithoutADeadlineHasNoCountdownAndIsCounted(t *testing.T) {
	r := newDetailRig(t)
	r.d.detail.PaymentExpiresAt = time.Time{}
	v, err := r.uc.Get(context.Background(), detailGrant, "RB-1")
	if err != nil || v.PaymentTTL != nil || r.m.counts[domain.MetricPaymentDeadlineMissing] != 1 {
		t.Fatalf("payment_ttl %+v (%v) metrics %v, want none and counted", v.PaymentTTL, err, r.m.counts)
	}
}
