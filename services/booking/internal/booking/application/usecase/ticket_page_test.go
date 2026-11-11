package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

var (
	now      = time.Date(2026, 11, 10, 19, 2, 0, 0, time.UTC)
	deadline = now.Add(3 * time.Minute)
	grant    = grantauth.Grant{Token: "gt_1", EventID: "evt_a", BookingID: "RB-1", QueueToken: "qt_1", DeviceHash: "h"}
	account  = grantauth.Grant{Token: "gt_2", EventID: "evt_a", BookingID: "RB-1", QueueToken: "qt_1", AccountID: "acc_1"}
	errDown  = errors.New("connection refused")
)

type fakeBookings struct {
	row    domain.Booking
	found  bool
	err    error
	claims [][2]string // (booking id, grant token) per call
}

func (f *fakeBookings) ClaimForSelection(_ context.Context, id, token string) (domain.Booking, bool, error) {
	f.claims = append(f.claims, [2]string{id, token})
	if f.row.Status == bookingstate.Granted {
		f.row.Status = bookingstate.Selecting // what the adapter's UPDATE does
	}
	return f.row, f.found, f.err
}

type fakeCatalog struct {
	event      domain.Event
	found      bool
	cats       []domain.Category
	account    *domain.Account
	err        error
	accountIDs []string // the account id of every call ("" for a guest)
}

func (f *fakeCatalog) Page(_ context.Context, _, accountID string) (domain.Event, []domain.Category, *domain.Account, bool, error) {
	f.accountIDs = append(f.accountIDs, accountID)
	return f.event, f.cats, f.account, f.found, f.err
}

type fakeStock struct {
	levels  map[string]int64
	missing []string
	err     error
}

func (f *fakeStock) Levels(context.Context, string, []string) (map[string]int64, []string, error) {
	return f.levels, f.missing, f.err
}

type fakeMetrics struct{ counts map[string]int }

func (m *fakeMetrics) Incr(name string) { m.counts[name]++ }

type rig struct {
	b  *fakeBookings
	c  *fakeCatalog
	s  *fakeStock
	m  *fakeMetrics
	uc *TicketPage
}

func newRig() *rig {
	r := &rig{
		b: &fakeBookings{found: true, row: domain.Booking{ID: "RB-1", EventID: "evt_a", Status: bookingstate.Granted, BookingExpiresAt: deadline}},
		c: &fakeCatalog{found: true, event: domain.Event{ID: "evt_a", Name: "Konser A", Date: "2026-12-25", Venue: "GBK", ImageURL: "https://cdn/a.jpg"},
			cats: []domain.Category{
				{ID: "cat_vip", Name: "VIP", Price: 1500000, MaxPerOrder: 3},
				{ID: "cat_fest", Name: "Festival", Price: 850000, MaxPerOrder: 3},
				{ID: "cat_trib", Name: "Tribune", Price: 500000, MaxPerOrder: 3},
			}},
		s: &fakeStock{levels: map[string]int64{"cat_vip": 12, "cat_fest": 240, "cat_trib": 0}},
		m: &fakeMetrics{counts: map[string]int{}},
	}
	r.uc = NewTicketPage(TicketPageDeps{Bookings: r.b, Catalog: r.c, Stock: r.s, Metrics: r.m, Clock: func() time.Time { return now }}, 3)
	return r
}

func wantAppError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var ae *apperror.AppError
	if !errors.As(err, &ae) || ae.HTTPStatus != status || ae.ErrorCode != code {
		t.Fatalf("err = %v, want %d %s", err, status, code)
	}
}

func TestAGrantForAnotherEventIsRejectedAsInvalid(t *testing.T) {
	r := newRig()
	_, err := r.uc.Get(context.Background(), grant, "evt_b")
	wantAppError(t, err, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid)
	if len(r.b.claims) != 0 {
		t.Fatal("a grant for another event must not claim anything")
	}
}

func TestTheFirstLoadClaimsTheBookingOfTheGrantAndKeepsItsDeadline(t *testing.T) {
	r := newRig()
	v, err := r.uc.Get(context.Background(), grant, "evt_a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.b.claims, [][2]string{{"RB-1", "gt_1"}}) {
		t.Fatalf("claims = %v, want one claim bound to the grant's booking and token", r.b.claims)
	}
	if v.Session.ExpiresAt != "2026-11-10T19:05:00Z" || v.Session.ServerTime != "2026-11-10T19:02:00Z" {
		t.Fatalf("session = %+v, want the deadline fixed at grant time", v.Session)
	}
}

func TestAReloadWhileSelectingRendersTheSameDeadline(t *testing.T) {
	r := newRig()
	first, err1 := r.uc.Get(context.Background(), grant, "evt_a")
	second, err2 := r.uc.Get(context.Background(), grant, "evt_a")
	if err1 != nil || err2 != nil || first.Session != second.Session {
		t.Fatalf("first %+v (%v), second %+v (%v)", first.Session, err1, second.Session, err2)
	}
}

func TestAHeldBookingRendersWithThePaymentDeadline(t *testing.T) {
	for _, st := range []bookingstate.Status{bookingstate.Held, bookingstate.AwaitingPayment, bookingstate.PendingCancellation} {
		t.Run(string(st), func(t *testing.T) {
			r := newRig()
			r.b.row.Status = st
			r.b.row.BookingExpiresAt = now.Add(-time.Minute) // the 5-minute deadline has passed
			r.b.row.PaymentExpiresAt = now.Add(8 * time.Minute)
			v, err := r.uc.Get(context.Background(), grant, "evt_a")
			if err != nil || v.Session.ExpiresAt != "2026-11-10T19:10:00Z" {
				t.Fatalf("session %+v (%v), want the payment deadline", v.Session, err)
			}
		})
	}
}

func TestAFinishedBookingIsRejectedAsInvalid(t *testing.T) {
	for _, st := range []bookingstate.Status{bookingstate.Lapsed, bookingstate.Expired, bookingstate.Confirmed, bookingstate.Abandoned} {
		t.Run(string(st), func(t *testing.T) {
			r := newRig()
			r.b.row.Status = st
			_, err := r.uc.Get(context.Background(), grant, "evt_a")
			wantAppError(t, err, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid)
		})
	}
}

func TestAGrantWithoutABookingRowIsRejectedAndCounted(t *testing.T) {
	r := newRig()
	r.b.found = false
	_, err := r.uc.Get(context.Background(), grant, "evt_a")
	wantAppError(t, err, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid)
	if r.m.counts[domain.MetricLostGrant] != 1 {
		t.Fatalf("metrics %v, want LostGrant counted", r.m.counts)
	}
}

func TestABookingOfAnotherEventIsRejectedAsInvalid(t *testing.T) {
	r := newRig()
	r.b.row.EventID = "evt_b"
	_, err := r.uc.Get(context.Background(), grant, "evt_a")
	wantAppError(t, err, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid)
}

func TestAMissingEventIsNotFound(t *testing.T) {
	r := newRig()
	r.c.found = false
	_, err := r.uc.Get(context.Background(), grant, "evt_a")
	wantAppError(t, err, http.StatusNotFound, domain.CodeNotFound)
}

func TestMaxPerOrderIsTheSmallestOfTheThreeLimits(t *testing.T) {
	cases := []struct {
		name       string
		catMax     int
		stock      int64
		perOrder   int
		wantPerCat int64
	}{
		{"plenty of seats", 3, 240, 3, 3},
		{"two seats left", 3, 2, 3, 2},
		{"category allows one", 1, 240, 3, 1},
		{"per-order limit lowered", 3, 240, 2, 2},
		{"sold out", 3, 0, 3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig()
			r.c.cats = []domain.Category{{ID: "cat_vip", Name: "VIP", Price: 1, MaxPerOrder: tc.catMax}}
			r.s.levels = map[string]int64{"cat_vip": tc.stock}
			r.uc = NewTicketPage(r.uc.d, tc.perOrder)
			v, err := r.uc.Get(context.Background(), grant, "evt_a")
			if err != nil || len(v.Categories) != 1 || v.Categories[0].MaxPerOrder != tc.wantPerCat {
				t.Fatalf("categories %+v (%v), want max_per_order %d", v.Categories, err, tc.wantPerCat)
			}
			if v.Rules.MaxTicketsPerOrder != tc.perOrder || !v.Rules.OneNIKPerTicket {
				t.Fatalf("rules %+v", v.Rules)
			}
		})
	}
}

func TestAMissingStockKeyIsStockUninitialized(t *testing.T) {
	r := newRig()
	r.s.missing = []string{"cat_fest"}
	_, err := r.uc.Get(context.Background(), grant, "evt_a")
	wantAppError(t, err, http.StatusServiceUnavailable, domain.CodeStockUninitialized)
	if r.m.counts[domain.MetricStockUninitialized] != 1 {
		t.Fatalf("metrics %v", r.m.counts)
	}
}

func TestANegativeStockIsShownAsZero(t *testing.T) {
	r := newRig()
	r.s.levels["cat_vip"] = -2
	v, err := r.uc.Get(context.Background(), grant, "evt_a")
	if err != nil || v.Categories[0].Available != 0 || v.Categories[0].MaxPerOrder != 0 {
		t.Fatalf("VIP %+v (%v), want 0 available", v.Categories, err)
	}
}

// The client counts down from expires_at with its clock corrected by server_time (contract v2.15): a deadline that
// has already passed (the sweep has not run yet) is sent as it is, and there is no ttl_seconds to go negative.
func TestThePageSendsTheDeadlineAsItIsAndNoSecondsLeft(t *testing.T) {
	r := newRig()
	r.b.row.BookingExpiresAt = now.Add(-2 * time.Second)
	v, err := r.uc.Get(context.Background(), grant, "evt_a")
	if err != nil || v.Session.ExpiresAt != "2026-11-10T19:01:58Z" || v.Session.ServerTime != "2026-11-10T19:02:00Z" {
		t.Fatalf("session %+v (%v), want the passed deadline and the server time", v.Session, err)
	}
	if b, _ := json.Marshal(v); strings.Contains(string(b), "ttl_seconds") {
		t.Fatalf("the response must not carry ttl_seconds: %s", b)
	}
}

func TestCategoriesKeepTheCatalogOrderAndFields(t *testing.T) {
	r := newRig()
	r.c.cats[0].Description = "Lebih dekat."
	v, err := r.uc.Get(context.Background(), grant, "evt_a")
	if err != nil {
		t.Fatal(err)
	}
	want := []CategoryView{
		{ID: "cat_vip", Name: "VIP", Description: "Lebih dekat.", Price: 1500000, Available: 12, MaxPerOrder: 3},
		{ID: "cat_fest", Name: "Festival", Price: 850000, Available: 240, MaxPerOrder: 3},
		{ID: "cat_trib", Name: "Tribune", Price: 500000, Available: 0, MaxPerOrder: 0},
	}
	if !reflect.DeepEqual(v.Categories, want) {
		t.Fatalf("categories\n got %+v\nwant %+v", v.Categories, want)
	}
	if v.Event != (EventView{ID: "evt_a", Name: "Konser A", Date: "2026-12-25", Venue: "GBK", ImageURL: "https://cdn/a.jpg"}) {
		t.Fatalf("event %+v", v.Event)
	}
}

func TestADatabaseOrRedisFailureIsA503(t *testing.T) {
	cases := map[string]func(r *rig){
		"claim":   func(r *rig) { r.b.err = errDown },
		"catalog": func(r *rig) { r.c.err = errDown },
		"stock":   func(r *rig) { r.s.err = errDown },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			breakIt(r)
			_, err := r.uc.Get(context.Background(), grant, "evt_a")
			wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
		})
	}
}

func strp(s string) *string { return &s }

func TestAnAccountGrantGetsTheAccountsEmailAndMaskedNIK(t *testing.T) {
	r := newRig()
	r.c.account = &domain.Account{Email: "buyer@example.com", NIKMasked: strp("3171********0022")}
	v, err := r.uc.Get(context.Background(), account, "evt_a")
	if err != nil || v.Account == nil || v.Account.Email != "buyer@example.com" || v.Account.NIKMasked == nil || *v.Account.NIKMasked != "3171********0022" {
		t.Fatalf("account = %+v (%v)", v.Account, err)
	}
	if !reflect.DeepEqual(r.c.accountIDs, []string{"acc_1"}) {
		t.Fatalf("catalog called with %v, want the grant's account id", r.c.accountIDs)
	}
}

func TestAnAccountWithoutANIKGetsNullForTheNIKButKeepsTheEmail(t *testing.T) {
	r := newRig()
	r.c.account = &domain.Account{Email: "buyer@example.com"}
	v, err := r.uc.Get(context.Background(), account, "evt_a")
	if err != nil || v.Account == nil || v.Account.NIKMasked != nil {
		t.Fatalf("account = %+v (%v), want nik_masked null", v.Account, err)
	}
	b, _ := json.Marshal(v.Account)
	if string(b) != `{"email":"buyer@example.com","nik_masked":null}` {
		t.Fatalf("json = %s", b)
	}
}

func TestAGuestGrantGetsNullAndTheCatalogIsAskedWithoutAnAccount(t *testing.T) {
	r := newRig()
	v, err := r.uc.Get(context.Background(), grant, "evt_a")
	if err != nil || v.Account != nil {
		t.Fatalf("account = %+v (%v), want null for a guest", v.Account, err)
	}
	if !reflect.DeepEqual(r.c.accountIDs, []string{""}) {
		t.Fatalf("catalog called with %v, want no account id for a guest", r.c.accountIDs)
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"account":null`) {
		t.Fatalf("the page must carry account:null for a guest, not omit it: %s", b)
	}
}

func TestAnAccountThatNoLongerExistsRendersThePageWithNullAccount(t *testing.T) {
	r := newRig() // account grant, but the catalog returns no account row
	v, err := r.uc.Get(context.Background(), account, "evt_a")
	if err != nil || v.Account != nil || len(v.Categories) == 0 {
		t.Fatalf("account = %+v categories = %d (%v), want the page without an account", v.Account, len(v.Categories), err)
	}
}

func TestThePageNeverCarriesAFullNIKOrAHash(t *testing.T) {
	r := newRig()
	r.c.account = &domain.Account{Email: "buyer@example.com", NIKMasked: strp("3171********0022")}
	v, _ := r.uc.Get(context.Background(), account, "evt_a")
	b, _ := json.Marshal(v.Account)
	var keys map[string]any
	_ = json.Unmarshal(b, &keys)
	if len(keys) != 2 || keys["email"] == nil || keys["nik_masked"] == nil {
		t.Fatalf("account keys = %v, want exactly email and nik_masked", keys)
	}
}
