package usecase

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

var (
	guestGrant = grantauth.Grant{Token: "gt_1", EventID: "evt_a", BookingID: "RB-1", QueueToken: "qt_1", DeviceHash: "h"}
	acctGrant  = grantauth.Grant{Token: "gt_2", EventID: "evt_a", BookingID: "RB-1", QueueToken: "qt_1", AccountID: "acc_1"}
)

// ---- fakes

type fakeOrders struct {
	status      domain.BookingStatus
	statusFound bool
	statusErr   error
	cats        []domain.Category
	catsErr     error
	identities  domain.Identities
	identErr    error

	placeOutcome domain.PlaceOutcome // when set, Place ends so without calling hold (the checks under the lock)
	placeErr     error               // returned after a taken hold
	conflict     bool                // ClaimConflict after a taken hold
	conflictAt   int

	statusCalls, catCalls, identCalls, placeCalls int
	identAccount                                  string
	identSaved                                    []string
	placed                                        domain.Placement
	placeCtxErr                                   error // ctx.Err() as Place saw it
}

func (f *fakeOrders) Status(context.Context, string, string) (domain.BookingStatus, bool, error) {
	f.statusCalls++
	return f.status, f.statusFound, f.statusErr
}

func (f *fakeOrders) Categories(context.Context, string) ([]domain.Category, error) {
	f.catCalls++
	return f.cats, f.catsErr
}

func (f *fakeOrders) Identities(_ context.Context, account string, saved []string) (domain.Identities, error) {
	f.identCalls++
	f.identAccount, f.identSaved = account, saved
	return f.identities, f.identErr
}

func (f *fakeOrders) Place(ctx context.Context, p domain.Placement, hold outbound.HoldFunc) (domain.PlaceResult, error) {
	f.placeCalls++
	f.placed = p
	f.placeCtxErr = ctx.Err()
	if f.placeOutcome != 0 {
		return domain.PlaceResult{Outcome: f.placeOutcome}, nil
	}
	ans, err := hold(ctx)
	if err != nil {
		return domain.PlaceResult{}, err
	}
	if !ans.Taken() {
		return domain.PlaceResult{Outcome: domain.Refused, Hold: ans}, nil
	}
	if f.placeErr != nil {
		return domain.PlaceResult{}, f.placeErr
	}
	if f.conflict {
		return domain.PlaceResult{Outcome: domain.ClaimConflict, ConflictIndex: f.conflictAt}, nil
	}
	return domain.PlaceResult{Outcome: domain.Placed}, nil
}

type fakeHolds struct {
	onHold         func() // runs while the hold is in flight
	answer         domain.HoldAnswer
	holdErr        error
	releaseMissing int
	releaseErr     error
	holds          []domain.HoldOrder
	releases       []domain.HoldOrder
}

func (f *fakeHolds) Hold(_ context.Context, _ string, o domain.HoldOrder) (domain.HoldAnswer, error) {
	f.holds = append(f.holds, o)
	if f.onHold != nil {
		f.onHold()
	}
	return f.answer, f.holdErr
}

func (f *fakeHolds) Release(_ context.Context, _ string, o domain.HoldOrder) (int, error) {
	f.releases = append(f.releases, o)
	return f.releaseMissing, f.releaseErr
}

type fakeQueue struct {
	snap domain.QueueSnapshot
	err  error
}

func (f *fakeQueue) Snapshot(context.Context, string) (domain.QueueSnapshot, error) {
	return f.snap, f.err
}

// fakeNIKs answers by the digits of the NIK, so a NIK typed with spaces is the same NIK.
type fakeNIKs struct {
	checks map[string]domain.NIKCheck
	errs   map[string]error
	calls  atomic.Int32
	typed  []string
	mu     sync.Mutex
	// afterCalls runs after every call (a test cancels the request here).
	afterCalls func()
	// barrier, when set, makes every call wait until that many calls are in flight: it proves they run in parallel.
	barrier *sync.WaitGroup
}

func digits(s string) string {
	return strings.Map(func(r rune) rune { return map[bool]rune{true: r, false: -1}[r >= '0' && r <= '9'] }, s)
}

func (f *fakeNIKs) Validate(_ context.Context, nik string) (domain.NIKCheck, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.typed = append(f.typed, nik)
	f.mu.Unlock()
	if f.barrier != nil {
		f.barrier.Done()
		done := make(chan struct{})
		go func() { f.barrier.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			return domain.NIKCheck{}, errors.New("calls are not running in parallel")
		}
	}
	if f.afterCalls != nil {
		defer f.afterCalls()
	}
	d := digits(nik)
	if err := f.errs[d]; err != nil {
		return domain.NIKCheck{}, err
	}
	if c, ok := f.checks[d]; ok {
		return c, nil
	}
	return domain.NIKCheck{NIKHmac: "hmac-" + d, NIKMasked: d[:4] + "**********" + d[14:], Valid: true, Status: "ACTIVE", Source: "verified"}, nil
}

type fakeReader struct {
	err   error
	calls []string
}

func (f *fakeReader) Get(_ context.Context, g grantauth.Grant, id string) (BookingView, error) {
	f.calls = append(f.calls, id+"@"+g.Token)
	return BookingView{BookingID: id, Status: "PENDING_PAYMENT"}, f.err
}

// ---- rig

type createRig struct {
	o  *fakeOrders
	h  *fakeHolds
	s  *fakeStock
	q  *fakeQueue
	n  *fakeNIKs
	r  *fakeReader
	m  *fakeMetrics
	uc *CreateBooking
}

const (
	nikA = "3171000000000022"
	nikB = "3171000000000033"
	nikC = "3171000000000044"
)

func newCreateRig() *createRig {
	r := &createRig{
		o: &fakeOrders{
			status: domain.BookingStatus{Status: bookingstate.Selecting}, statusFound: true,
			cats: []domain.Category{
				{ID: "cat_vip", Name: "VIP", Price: 1500000, MaxPerOrder: 3},
				{ID: "cat_fest", Name: "Festival", Price: 850000, MaxPerOrder: 3},
			},
		},
		h: &fakeHolds{answer: domain.HoldAnswer{Code: domain.HoldHeld, Index: -1}},
		s: &fakeStock{levels: map[string]int64{"cat_vip": 4, "cat_fest": 0}},
		q: &fakeQueue{snap: domain.QueueSnapshot{State: "OPEN"}},
		n: &fakeNIKs{},
		r: &fakeReader{},
		m: &fakeMetrics{counts: map[string]int{}},
	}
	seq := 0
	r.uc = NewCreateBooking(CreateBookingDeps{Orders: r.o, Holds: r.h, Stock: r.s, Queue: r.q, NIKs: r.n, Reader: r.r, Metrics: r.m,
		NewID: func() string { seq++; return "BI-" + string(rune('0'+seq)) }}, 3)
	return r
}

func seat(cat, nik string) SeatRequest { return SeatRequest{CategoryID: cat, NIK: nik} }

func order(seats ...SeatRequest) CreateRequest {
	return CreateRequest{EventID: "evt_a", Tickets: seats, ContactEmail: "buyer@example.com"}
}

func (r *createRig) create(g grantauth.Grant, req CreateRequest) (CreateResult, error) {
	return r.uc.Create(context.Background(), g, req)
}

func wantIndex(t *testing.T, err error, status int, code string, index int) {
	t.Helper()
	wantAppError(t, err, status, code)
	var ae *apperror.AppError
	_ = errors.As(err, &ae)
	m, ok := ae.Data.(map[string]any)
	if !ok || m["index"] != index {
		t.Fatalf("data = %#v, want index %d", ae.Data, index)
	}
}

func wantData(t *testing.T, err error, want map[string]any) {
	t.Helper()
	var ae *apperror.AppError
	if !errors.As(err, &ae) || !reflect.DeepEqual(ae.Data, want) {
		t.Fatalf("data = %#v, want %#v", ae.Data, want)
	}
}

func (r *createRig) touchedNothingBeyondTheStatus(t *testing.T) {
	t.Helper()
	if r.o.catCalls+r.o.identCalls+r.o.placeCalls != 0 || r.n.calls.Load() != 0 || len(r.h.holds) != 0 {
		t.Fatalf("categories=%d identities=%d place=%d niks=%d holds=%d, want none", r.o.catCalls, r.o.identCalls, r.o.placeCalls, r.n.calls.Load(), len(r.h.holds))
	}
}

// ---- the status is read before the body (step 1a)

func TestABookingAlreadyHeldOrLaterAnswers200WithTheExistingBookingWhateverTheBodySays(t *testing.T) {
	for _, st := range []bookingstate.Status{bookingstate.Held, bookingstate.AwaitingPayment, bookingstate.PendingCancellation, bookingstate.Confirmed} {
		t.Run(string(st), func(t *testing.T) {
			r := newCreateRig()
			r.o.status.Status = st
			res, err := r.create(guestGrant, CreateRequest{ContactEmail: "not-an-email"}) // an invalid body
			if err != nil || res.Created || res.View.BookingID != "RB-1" {
				t.Fatalf("result = %+v (%v), want the existing booking, not created", res, err)
			}
			r.touchedNothingBeyondTheStatus(t)
			if !reflect.DeepEqual(r.r.calls, []string{"RB-1@gt_1"}) {
				t.Fatalf("view read = %v, want the grant's booking", r.r.calls)
			}
		})
	}
}

func TestTheStatusAnswersComeBeforeTheBodyIsRead(t *testing.T) {
	cases := []struct {
		name   string
		status domain.BookingStatus
		found  bool
		http   int
		code   string
	}{
		{"never opened, in time", domain.BookingStatus{Status: bookingstate.Granted}, true, http.StatusConflict, domain.CodeBookingStatusInvalid},
		{"never opened, time over", domain.BookingStatus{Status: bookingstate.Granted, PastDeadline: true}, true, http.StatusConflict, domain.CodeBookingExpired},
		{"selecting, time over", domain.BookingStatus{Status: bookingstate.Selecting, PastDeadline: true}, true, http.StatusConflict, domain.CodeBookingExpired},
		{"lapsed", domain.BookingStatus{Status: bookingstate.Lapsed}, true, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid},
		{"expired", domain.BookingStatus{Status: bookingstate.Expired}, true, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid},
		{"abandoned", domain.BookingStatus{Status: bookingstate.Abandoned}, true, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid},
		{"no row", domain.BookingStatus{}, false, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newCreateRig()
			r.o.status, r.o.statusFound = tc.status, tc.found
			_, err := r.create(guestGrant, CreateRequest{})
			wantAppError(t, err, tc.http, tc.code)
			r.touchedNothingBeyondTheStatus(t)
			if !tc.found && r.m.counts[domain.MetricLostGrant] != 1 {
				t.Fatalf("metrics %v, want LostGrant counted", r.m.counts)
			}
		})
	}
}

func TestAStatusReadFailureIs503(t *testing.T) {
	r := newCreateRig()
	r.o.statusErr = errDown
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
}

// ---- the body (step 2): refused before anything is resolved, checked or held

func TestEachBodyRuleAnswers400WithItsCodeAndSeatIndexAndTouchesNothing(t *testing.T) {
	two := func(a, b SeatRequest) CreateRequest { return order(seat("cat_vip", nikA), a, b) }
	cases := []struct {
		name  string
		g     grantauth.Grant
		req   CreateRequest
		code  string
		index int // -1: no index
	}{
		{"no seats", guestGrant, order(), "VALIDATION_ERROR", -1},
		{"a bad email", guestGrant, CreateRequest{EventID: "evt_a", Tickets: []SeatRequest{seat("cat_vip", nikA)}, ContactEmail: "nope"}, "VALIDATION_ERROR", -1},
		{"no email", guestGrant, CreateRequest{EventID: "evt_a", Tickets: []SeatRequest{seat("cat_vip", nikA)}}, "VALIDATION_ERROR", -1},
		{"a seat without a category", guestGrant, order(seat("", nikA)), "VALIDATION_ERROR", 0},
		{"a seat with no NIK source", guestGrant, order(SeatRequest{CategoryID: "cat_vip"}), "VALIDATION_ERROR", 0},
		{"a seat with two NIK sources", acctGrant, order(SeatRequest{CategoryID: "cat_vip", NIK: nikA, SavedNIKID: "sn_1"}), "VALIDATION_ERROR", 0},
		{"a seat with all three sources", acctGrant, order(SeatRequest{CategoryID: "cat_vip", NIK: nikA, SavedNIKID: "sn_1", UseAccountNIK: true}), "VALIDATION_ERROR", 0},
		{"use_account_nik on two seats", acctGrant, two(SeatRequest{CategoryID: "cat_vip", UseAccountNIK: true}, SeatRequest{CategoryID: "cat_vip", UseAccountNIK: true}), "VALIDATION_ERROR", 2},
		{"use_account_nik from a guest", guestGrant, order(SeatRequest{CategoryID: "cat_vip", UseAccountNIK: true}), "VALIDATION_ERROR", 0},
		{"saved_nik_id from a guest", guestGrant, order(SeatRequest{CategoryID: "cat_vip", SavedNIKID: "sn_1"}), "VALIDATION_ERROR", 0},
		{"a NIK that is too short", guestGrant, order(seat("cat_vip", "317100000000002")), "VALIDATION_ERROR", 0},
		{"a NIK that is too long", guestGrant, two(seat("cat_vip", nikB), seat("cat_vip", "31710000000000221")), "VALIDATION_ERROR", 2},
		{"a NIK of letters", guestGrant, order(seat("cat_vip", "abcdefghijklmnop")), "VALIDATION_ERROR", 0},
		{"more than three seats in total", guestGrant, order(seat("cat_vip", nikA), seat("cat_vip", nikB), seat("cat_fest", nikC), seat("cat_fest", "3171000000000055")), domain.CodeQtyExceeded, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newCreateRig()
			_, err := r.create(tc.g, tc.req)
			if tc.index >= 0 {
				wantIndex(t, err, http.StatusBadRequest, tc.code, tc.index)
			} else {
				wantAppError(t, err, http.StatusBadRequest, tc.code)
			}
			r.touchedNothingBeyondTheStatus(t)
		})
	}
}

func TestAnEventIdOfAnotherEventIsRejectedAsAnInvalidGrant(t *testing.T) {
	r := newCreateRig()
	req := order(seat("cat_vip", nikA))
	req.EventID = "evt_b"
	_, err := r.create(guestGrant, req)
	wantAppError(t, err, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid)
	r.touchedNothingBeyondTheStatus(t)
}

func TestATypedNIKWithSpacesDotsOrDashesIsAccepted(t *testing.T) {
	for _, typed := range []string{"3171 0000 0000 0022", "3171-0000-0000-0022", "3171.0000.0000.0022", " 3171000000000022 "} {
		r := newCreateRig()
		if _, err := r.create(guestGrant, order(seat("cat_vip", typed))); err != nil {
			t.Fatalf("%q: %v", typed, err)
		}
		if !reflect.DeepEqual(r.n.typed, []string{typed}) {
			t.Fatalf("nik-verify got %v, want the NIK exactly as typed (it normalises again)", r.n.typed)
		}
	}
}

func TestAnUnknownCategoryIsRefusedWithItsSeatIndex(t *testing.T) {
	r := newCreateRig()
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA), seat("cat_nope", nikB)))
	wantIndex(t, err, http.StatusBadRequest, "VALIDATION_ERROR", 1)
	if r.n.calls.Load() != 0 || len(r.h.holds) != 0 {
		t.Fatal("an unknown category must stop before any NIK check or hold")
	}
}

// ---- the NIK of every seat (step 4)

func TestUseAccountNIKUsesTheAccountsOwnVerifiedNIKWithoutAskingNikVerify(t *testing.T) {
	r := newCreateRig()
	r.o.identities = domain.Identities{Own: &domain.Identity{NIKHmac: "own-hmac", NIKMasked: "3171**********22"}}
	res, err := r.create(acctGrant, order(SeatRequest{CategoryID: "cat_vip", UseAccountNIK: true}))
	if err != nil || !res.Created {
		t.Fatalf("result = %+v (%v)", res, err)
	}
	if r.n.calls.Load() != 0 || r.o.identAccount != "acc_1" {
		t.Fatalf("nik-verify calls = %d, identities for %q", r.n.calls.Load(), r.o.identAccount)
	}
	if got := r.o.placed.Seats[0]; got.NIKHmac != "own-hmac" || got.NIKMasked != "3171**********22" {
		t.Fatalf("seat = %+v, want the account's stored proof", got)
	}
}

func TestUseAccountNIKWithoutAVerifiedNIKIsRefusedWithItsIndex(t *testing.T) {
	r := newCreateRig() // identities: no own NIK
	_, err := r.create(acctGrant, order(SeatRequest{CategoryID: "cat_vip", UseAccountNIK: true}))
	wantIndex(t, err, http.StatusBadRequest, "VALIDATION_ERROR", 0)
	if len(r.h.holds) != 0 {
		t.Fatal("no hold without a NIK")
	}
}

func TestASavedNIKOfThisAccountResolvesWithoutAskingNikVerify(t *testing.T) {
	r := newCreateRig()
	r.o.identities = domain.Identities{Saved: map[string]domain.Identity{"sn_1": {NIKHmac: "saved-hmac", NIKMasked: "3171**********33"}}}
	_, err := r.create(acctGrant, order(SeatRequest{CategoryID: "cat_vip", SavedNIKID: "sn_1"}))
	if err != nil || r.n.calls.Load() != 0 || !reflect.DeepEqual(r.o.identSaved, []string{"sn_1"}) {
		t.Fatalf("err=%v nik-verify calls=%d saved ids asked=%v", err, r.n.calls.Load(), r.o.identSaved)
	}
	if got := r.o.placed.Seats[0]; got.NIKHmac != "saved-hmac" {
		t.Fatalf("seat = %+v", got)
	}
}

// An id of another account, a deleted one and an unknown one look the same: no id of another account leaks.
func TestASavedNIKIdThatIsNotThisAccountsIsRefusedLikeAnyOtherBadSeat(t *testing.T) {
	r := newCreateRig()
	_, err := r.create(acctGrant, order(seat("cat_vip", nikA), SeatRequest{CategoryID: "cat_vip", SavedNIKID: "sn_of_someone_else"}))
	wantIndex(t, err, http.StatusBadRequest, "VALIDATION_ERROR", 1)
	if r.n.calls.Load() != 0 {
		t.Fatal("no NIK may be sent to Dukcapil for an order that is already refused")
	}
}

func TestTypedNIKsAreCheckedByNikVerifyInParallel(t *testing.T) {
	r := newCreateRig()
	var barrier sync.WaitGroup
	barrier.Add(3)
	r.n.barrier = &barrier
	if _, err := r.create(guestGrant, order(seat("cat_vip", nikA), seat("cat_vip", nikB), seat("cat_fest", nikC))); err != nil {
		t.Fatalf("three typed NIKs must be in flight together: %v", err)
	}
	if r.n.calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", r.n.calls.Load())
	}
}

func TestANIKThatIsNotValidIsRefusedWithItsSeatAndCode(t *testing.T) {
	for status, code := range map[string]string{"NOT_FOUND": "NIK_NOT_FOUND", "INACTIVE": "NIK_INACTIVE"} {
		t.Run(status, func(t *testing.T) {
			r := newCreateRig()
			r.n.checks = map[string]domain.NIKCheck{nikB: {NIKHmac: "x", NIKMasked: "m", Valid: false, Status: status}}
			_, err := r.create(guestGrant, order(seat("cat_vip", nikA), seat("cat_vip", nikB)))
			wantIndex(t, err, http.StatusBadRequest, code, 1)
			if len(r.h.holds) != 0 || r.o.placeCalls != 0 {
				t.Fatal("no hold for a refused NIK")
			}
		})
	}
}

func TestWhenSeveralNIKsAreNotValidTheFirstSeatIsReported(t *testing.T) {
	r := newCreateRig()
	bad := domain.NIKCheck{NIKHmac: "x", NIKMasked: "m", Valid: false, Status: "NOT_FOUND"}
	r.n.checks = map[string]domain.NIKCheck{nikA: bad, nikC: bad}
	_, err := r.create(guestGrant, order(seat("cat_vip", nikC), seat("cat_vip", nikB), seat("cat_vip", nikA)))
	wantIndex(t, err, http.StatusBadRequest, "NIK_NOT_FOUND", 0)
}

func TestNikVerifyBeingUnavailableIs503AndNothingIsHeld(t *testing.T) {
	r := newCreateRig()
	r.n.errs = map[string]error{nikB: domain.ErrNIKUnavailable}
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA), seat("cat_vip", nikB)))
	wantAppError(t, err, http.StatusServiceUnavailable, "DUKCAPIL_UNAVAILABLE")
	if len(r.h.holds) != 0 || r.o.placeCalls != 0 {
		t.Fatal("no hold without proof (fail closed)")
	}
}

func TestAnUnexpectedNikVerifyErrorIsAlsoFailClosed(t *testing.T) {
	r := newCreateRig()
	r.n.errs = map[string]error{nikA: errors.New("boom")}
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	wantAppError(t, err, http.StatusServiceUnavailable, "DUKCAPIL_UNAVAILABLE")
}

// The same NIK typed two ways, or typed and also saved, is one NIK: compared by hash after every seat is resolved.
func TestTheSameNIKTwiceInOneOrderIsRefusedWhateverItsSource(t *testing.T) {
	t.Run("typed twice with different formatting", func(t *testing.T) {
		r := newCreateRig()
		_, err := r.create(guestGrant, order(seat("cat_vip", "3171 0000 0000 0022"), seat("cat_fest", nikA)))
		wantIndex(t, err, http.StatusBadRequest, "VALIDATION_ERROR", 1)
		if len(r.h.holds) != 0 {
			t.Fatal("no hold for a duplicate NIK")
		}
	})
	t.Run("typed and also the account's own", func(t *testing.T) {
		r := newCreateRig()
		r.o.identities = domain.Identities{Own: &domain.Identity{NIKHmac: "hmac-" + nikA, NIKMasked: "m"}}
		_, err := r.create(acctGrant, order(SeatRequest{CategoryID: "cat_vip", UseAccountNIK: true}, seat("cat_vip", nikA)))
		wantIndex(t, err, http.StatusBadRequest, "VALIDATION_ERROR", 1)
	})
	t.Run("typed and also saved", func(t *testing.T) {
		r := newCreateRig()
		r.o.identities = domain.Identities{Saved: map[string]domain.Identity{"sn_1": {NIKHmac: "hmac-" + nikB, NIKMasked: "m"}}}
		_, err := r.create(acctGrant, order(seat("cat_vip", nikB), SeatRequest{CategoryID: "cat_vip", SavedNIKID: "sn_1"}))
		wantIndex(t, err, http.StatusBadRequest, "VALIDATION_ERROR", 1)
	})
}

func TestACategoriesOrIdentitiesReadFailureIs503(t *testing.T) {
	r := newCreateRig()
	r.o.catsErr = errDown
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")

	r = newCreateRig()
	r.o.identErr = errDown
	_, err = r.create(acctGrant, order(SeatRequest{CategoryID: "cat_vip", UseAccountNIK: true}))
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
}

// ---- the hold and the transaction (steps 5-9)

func TestAnOrderIsPlacedWithItsSeatsInTheOrderSentAndTheHoldAggregatedPerCategory(t *testing.T) {
	r := newCreateRig()
	res, err := r.create(guestGrant, order(seat("cat_fest", nikA), seat("cat_vip", nikB), seat("cat_fest", nikC)))
	if err != nil || !res.Created || res.View.BookingID != "RB-1" {
		t.Fatalf("result = %+v (%v), want a created booking", res, err)
	}
	p := r.o.placed
	if p.BookingID != "RB-1" || p.GrantToken != "gt_1" || p.EventID != "evt_a" || p.ContactEmail != "buyer@example.com" || len(p.Seats) != 3 {
		t.Fatalf("placement = %+v", p)
	}
	want := []domain.Seat{
		{ID: "BI-1", CategoryID: "cat_fest", CategoryName: "Festival", UnitPrice: 850000, NIKHmac: "hmac-" + nikA, NIKMasked: "3171**********22"},
		{ID: "BI-2", CategoryID: "cat_vip", CategoryName: "VIP", UnitPrice: 1500000, NIKHmac: "hmac-" + nikB, NIKMasked: "3171**********33"},
		{ID: "BI-3", CategoryID: "cat_fest", CategoryName: "Festival", UnitPrice: 850000, NIKHmac: "hmac-" + nikC, NIKMasked: "3171**********44"},
	}
	if !reflect.DeepEqual(p.Seats, want) {
		t.Fatalf("seats\n got %+v\nwant %+v", p.Seats, want)
	}
	wantHold := domain.HoldOrder{
		Categories: []domain.CategoryQty{{CategoryID: "cat_fest", Qty: 2}, {CategoryID: "cat_vip", Qty: 1}},
		NIKs:       []string{"hmac-" + nikA, "hmac-" + nikB, "hmac-" + nikC},
	}
	if len(r.h.holds) != 1 || !reflect.DeepEqual(r.h.holds[0], wantHold) {
		t.Fatalf("hold = %+v, want %+v", r.h.holds, wantHold)
	}
	if len(r.h.releases) != 0 {
		t.Fatal("a placed booking keeps its hold")
	}
	if !reflect.DeepEqual(r.r.calls, []string{"RB-1@gt_1"}) {
		t.Fatalf("the 201 body is the booking read after the commit, got %v", r.r.calls)
	}
}

func TestTheBookingsAccountIsNeverTakenFromTheRequest(t *testing.T) {
	r := newCreateRig()
	if _, err := r.create(acctGrant, order(seat("cat_vip", nikA))); err != nil {
		t.Fatal(err)
	}
	if r.o.placed.BookingID != acctGrant.BookingID || r.o.placed.GrantToken != acctGrant.Token {
		t.Fatalf("placement = %+v, want the grant's booking", r.o.placed)
	}
}

func TestWhatThePlacementChecksUnderTheLockIsAnsweredLikeTheFirstRead(t *testing.T) {
	cases := []struct {
		outcome domain.PlaceOutcome
		http    int
		code    string
		created bool
	}{
		{domain.AlreadyPlaced, http.StatusOK, "", false},
		{domain.Gone, http.StatusUnauthorized, grantauth.CodeGrantTokenInvalid, false},
		{domain.NotOpened, http.StatusConflict, domain.CodeBookingStatusInvalid, false},
		{domain.TimeOver, http.StatusConflict, domain.CodeBookingExpired, false},
	}
	for _, tc := range cases {
		r := newCreateRig()
		r.o.placeOutcome = tc.outcome
		res, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
		if tc.code == "" {
			if err != nil || res.Created || res.View.BookingID != "RB-1" {
				t.Fatalf("outcome %v: result = %+v (%v), want the existing booking", tc.outcome, res, err)
			}
		} else {
			wantAppError(t, err, tc.http, tc.code)
		}
		if len(r.h.holds) != 0 || len(r.h.releases) != 0 {
			t.Fatalf("outcome %v: no hold was taken, so nothing may be held or released", tc.outcome)
		}
	}
}

// ---- hold refusals: the booking stays SELECTING and the hold is not released (nothing was taken)

func TestNoStockWhileAnotherCategoryHasSeatsIsPartlySoldOut(t *testing.T) {
	r := newCreateRig()
	r.h.answer = domain.HoldAnswer{Code: domain.HoldNoStock, Index: 1}
	r.s.levels = map[string]int64{"cat_vip": 4, "cat_fest": 1} // fest is short for 2 seats, vip still has seats
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA), seat("cat_fest", nikB), seat("cat_fest", nikC)))
	wantAppError(t, err, http.StatusConflict, domain.CodeInsufficientStock)
	wantData(t, err, map[string]any{"category_id": "cat_fest", "available": int64(1), "requested": int64(2)})
	if len(r.h.releases) != 0 {
		t.Fatal("a refused hold took nothing: nothing to release")
	}
}

func TestNoStockWithEveryCategoryAtZeroIsTotallySoldOut(t *testing.T) {
	cases := []struct {
		name  string
		snap  domain.QueueSnapshot
		qerr  error
		state string
		at    any
	}{
		{"full booked", domain.QueueSnapshot{State: "FULL_BOOKED", FullBookedAt: time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)}, nil, "FULL_BOOKED", "2026-11-10T19:00:00Z"},
		{"the queue still says open (one tick behind the stock)", domain.QueueSnapshot{State: "OPEN"}, nil, "FULL_BOOKED", nil},
		{"closed stays closed", domain.QueueSnapshot{State: "CLOSED", FullBookedAt: time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)}, nil, "CLOSED", "2026-11-10T19:00:00Z"},
		{"the queue cannot be read", domain.QueueSnapshot{}, errDown, "FULL_BOOKED", nil},
		{"the state is empty", domain.QueueSnapshot{}, nil, "FULL_BOOKED", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newCreateRig()
			r.h.answer = domain.HoldAnswer{Code: domain.HoldNoStock, Index: 0}
			r.s.levels = map[string]int64{"cat_vip": 0, "cat_fest": -1}
			r.q.snap, r.q.err = tc.snap, tc.qerr
			_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
			wantAppError(t, err, http.StatusConflict, domain.CodeTicketsUnavailable)
			wantData(t, err, map[string]any{"queue_state": tc.state, "full_booked_at": tc.at})
		})
	}
}

func TestAMissingStockKeyInTheSoldOutReadIsUninitializedNeverSoldOut(t *testing.T) {
	r := newCreateRig()
	r.h.answer = domain.HoldAnswer{Code: domain.HoldNoStock, Index: 0}
	r.s.levels = map[string]int64{"cat_vip": 0}
	r.s.missing = []string{"cat_fest"}
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	wantAppError(t, err, http.StatusServiceUnavailable, domain.CodeStockUninitialized)
	if r.m.counts[domain.MetricStockUninitialized] != 1 {
		t.Fatalf("metrics %v", r.m.counts)
	}
}

func TestTheSoldOutReadFailingIs503(t *testing.T) {
	r := newCreateRig()
	r.h.answer = domain.HoldAnswer{Code: domain.HoldNoStock, Index: 0}
	r.s.err = errDown
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
}

func TestATakenNIKIs409WithTheSeatIndex(t *testing.T) {
	r := newCreateRig()
	r.h.answer = domain.HoldAnswer{Code: domain.HoldNIKTaken, Index: 1}
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA), seat("cat_vip", nikB)))
	wantIndex(t, err, http.StatusConflict, domain.CodeNIKAlreadyUsed, 1)
}

func TestTheOtherHoldRefusalsAre503(t *testing.T) {
	cases := []struct {
		answer domain.HoldAnswer
		code   string
		metric string
	}{
		{domain.HoldAnswer{Code: domain.HoldStockUninitialized, Index: 0}, domain.CodeStockUninitialized, domain.MetricStockUninitialized},
		{domain.HoldAnswer{Code: domain.HoldStockNegative, Index: 0}, domain.CodeStockNegative, domain.MetricStockNegative},
		{domain.HoldAnswer{Code: "QTY_EXCEEDED", Index: -1}, "INTERNAL_ERROR", ""},
		{domain.HoldAnswer{Code: "SOMETHING_NEW", Index: -1}, "INTERNAL_ERROR", ""},
	}
	for _, tc := range cases {
		t.Run(tc.answer.Code, func(t *testing.T) {
			r := newCreateRig()
			r.h.answer = tc.answer
			_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
			wantAppError(t, err, http.StatusServiceUnavailable, tc.code)
			if tc.metric != "" && r.m.counts[tc.metric] != 1 {
				t.Fatalf("metrics %v, want %s", r.m.counts, tc.metric)
			}
			if len(r.h.releases) != 0 {
				t.Fatal("nothing was taken: nothing to release")
			}
		})
	}
}

// ---- compensation: Redis is not in the transaction

func TestAFailureAfterTheHoldGivesTheHoldBackOnceAndAnswers503(t *testing.T) {
	r := newCreateRig()
	r.o.placeErr = errDown
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA), seat("cat_fest", nikB)))
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
	if len(r.h.releases) != 1 || !reflect.DeepEqual(r.h.releases[0], r.h.holds[0]) {
		t.Fatalf("releases = %+v, want exactly the hold that was taken", r.h.releases)
	}
	if r.m.counts[domain.MetricHoldCompensated] != 1 {
		t.Fatalf("metrics %v", r.m.counts)
	}
}

func TestAClaimConflictGivesTheHoldBackAndAnswersNIKAlreadyUsed(t *testing.T) {
	r := newCreateRig()
	r.o.conflict, r.o.conflictAt = true, 1
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA), seat("cat_vip", nikB)))
	wantIndex(t, err, http.StatusConflict, domain.CodeNIKAlreadyUsed, 1)
	if len(r.h.releases) != 1 || r.m.counts[domain.MetricNIKClaimConflict] != 1 {
		t.Fatalf("releases = %d metrics %v, want the hold released and the conflict counted", len(r.h.releases), r.m.counts)
	}
}

func TestAReleaseThatFailsIsCountedAndTheAnswerIsStillThe503(t *testing.T) {
	r := newCreateRig()
	r.o.placeErr = errDown
	r.h.releaseErr = errDown
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
	if r.m.counts[domain.MetricHoldCompensationFailed] != 1 || r.m.counts[domain.MetricHoldCompensated] != 0 {
		t.Fatalf("metrics %v, want HoldCompensationFailed only", r.m.counts)
	}
}

func TestAReleaseThatFindsTheStockKeyGoneIsCounted(t *testing.T) {
	r := newCreateRig()
	r.o.placeErr = errDown
	r.h.releaseMissing = 1
	_, _ = r.create(guestGrant, order(seat("cat_vip", nikA)))
	if r.m.counts[domain.MetricReleaseStockMissing] != 1 {
		t.Fatalf("metrics %v", r.m.counts)
	}
}

// Releasing the stock of a booking that may have committed would oversell; releasing nothing only under-sells until
// reconciliation. So an unknown commit keeps the hold.
func TestACommitWithAnUnknownResultKeepsTheHold(t *testing.T) {
	r := newCreateRig()
	r.o.placeErr = errors.Join(errDown, outbound.ErrCommitUnknown)
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
	if len(r.h.releases) != 0 || r.m.counts[domain.MetricHoldCommitUnknown] != 1 {
		t.Fatalf("releases = %d metrics %v, want the hold kept and counted", len(r.h.releases), r.m.counts)
	}
}

// If the hold's answer never arrived, it may or may not have been taken: releasing could add stock out of nothing.
func TestAHoldWhoseAnswerNeverArrivedIsNotReleased(t *testing.T) {
	r := newCreateRig()
	r.h.holdErr = errDown
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
	if len(r.h.releases) != 0 || r.m.counts[domain.MetricHoldUnknown] != 1 {
		t.Fatalf("releases = %d metrics %v", len(r.h.releases), r.m.counts)
	}
}

func TestADatabaseFailureBeforeTheHoldReleasesNothing(t *testing.T) {
	r := newCreateRig()
	r.o.placeOutcome = 0
	failing := &failingPlace{fakeOrders: r.o}
	r.uc.d.Orders = failing
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	wantAppError(t, err, http.StatusServiceUnavailable, "INTERNAL_ERROR")
	if len(r.h.holds) != 0 || len(r.h.releases) != 0 {
		t.Fatal("the hold never ran, so nothing may be released")
	}
}

type failingPlace struct{ *fakeOrders }

func (f *failingPlace) Place(context.Context, domain.Placement, outbound.HoldFunc) (domain.PlaceResult, error) {
	return domain.PlaceResult{}, errDown
}

// The booking is HELD and committed: a failure to render it must not release the seats. The client retries and gets 200.
func TestAFailureToRenderThePlacedBookingKeepsTheHold(t *testing.T) {
	r := newCreateRig()
	r.r.err = errDown
	_, err := r.create(guestGrant, order(seat("cat_vip", nikA)))
	if err == nil {
		t.Fatal("want the read error")
	}
	if len(r.h.releases) != 0 {
		t.Fatal("a committed booking keeps its hold")
	}
}

func TestACancelDuringTheHoldStillGivesATakenHoldBackWhenTheTransactionFails(t *testing.T) {
	r := newCreateRig()
	ctx, cancel := context.WithCancel(context.Background())
	r.h.onHold = cancel
	r.o.placeErr = errDown
	if _, err := r.uc.Create(ctx, guestGrant, order(seat("cat_vip", nikA))); err == nil {
		t.Fatal("want an error")
	}
	if len(r.h.releases) != 1 {
		t.Fatalf("releases = %d, want the hold given back even though the request was cancelled", len(r.h.releases))
	}
}

// ---- the body is read only after the status (a retry answers 200 even when the body is garbage)

func TestTheBodyIsNotParsedForABookingThatIsAlreadyHeld(t *testing.T) {
	r := newCreateRig()
	r.o.status.Status = bookingstate.Held
	parsed := false
	res, err := r.uc.CreateFrom(context.Background(), guestGrant, func() (CreateRequest, error) {
		parsed = true
		return CreateRequest{}, errors.New("malformed json")
	})
	if err != nil || res.Created || parsed {
		t.Fatalf("result = %+v (%v) parsed=%v, want 200 without reading the body", res, err, parsed)
	}
}

func TestAMalformedBodyOnASelectingBookingIsAValidationError(t *testing.T) {
	r := newCreateRig()
	_, err := r.uc.CreateFrom(context.Background(), guestGrant, func() (CreateRequest, error) {
		return CreateRequest{}, errors.New("malformed json")
	})
	wantAppError(t, err, http.StatusBadRequest, "VALIDATION_ERROR")
	r.touchedNothingBeyondTheStatus(t)
}

func TestTheBodyIsNotParsedWhenTheTimeIsOver(t *testing.T) {
	r := newCreateRig()
	r.o.status.PastDeadline = true
	parsed := false
	_, err := r.uc.CreateFrom(context.Background(), guestGrant, func() (CreateRequest, error) { parsed = true; return CreateRequest{}, nil })
	wantAppError(t, err, http.StatusConflict, domain.CodeBookingExpired)
	if parsed {
		t.Fatal("the status answers come before the body is read")
	}
}

// Once the hold starts, a client that hangs up (or a Lambda deadline) must not leave the answer unknown: Place runs on a
// context detached from the request, with its own short deadline.
func TestTheHoldAndTheTransactionSurviveACancelOfTheRequestThatStartsThem(t *testing.T) {
	r := newCreateRig()
	ctx, cancel := context.WithCancel(context.Background())
	r.n.afterCalls = cancel // the client hangs up while the NIKs are being checked, before the hold
	_, err := r.uc.Create(ctx, guestGrant, order(seat("cat_vip", nikA)))
	if err == nil {
		t.Fatal("a request cancelled before the hold must not place a booking")
	}
	if r.o.placeCalls != 0 || len(r.h.holds) != 0 {
		t.Fatalf("place=%d holds=%d, want nothing held for a customer who left", r.o.placeCalls, len(r.h.holds))
	}
}

func TestPlaceRunsOnAContextThatTheRequestCannotCancel(t *testing.T) {
	r := newCreateRig()
	ctx, cancel := context.WithCancel(context.Background())
	r.h.onHold = cancel // the request is cancelled while the hold is running
	res, err := r.uc.Create(ctx, guestGrant, order(seat("cat_vip", nikA)))
	if err != nil || !res.Created {
		t.Fatalf("result = %+v (%v), want the booking placed although the request was cancelled mid-hold", res, err)
	}
	if r.o.placeCtxErr != nil {
		t.Fatalf("Place saw ctx error %v, want a context the request cannot cancel", r.o.placeCtxErr)
	}
}

func TestPlaceHasItsOwnDeadline(t *testing.T) {
	r := newCreateRig()
	var deadline bool
	r.uc.d.Orders = &deadlineProbe{fakeOrders: r.o, saw: &deadline}
	_, _ = r.create(guestGrant, order(seat("cat_vip", nikA)))
	if !deadline {
		t.Fatal("Place must run under its own deadline, so a stuck dependency cannot hold the row lock for ever")
	}
}

type deadlineProbe struct {
	*fakeOrders
	saw *bool
}

func (d *deadlineProbe) Place(ctx context.Context, p domain.Placement, h outbound.HoldFunc) (domain.PlaceResult, error) {
	_, *d.saw = ctx.Deadline()
	return d.fakeOrders.Place(ctx, p, h)
}

func TestAnEventIdThatIsMissingIsAValidationErrorNotAnInvalidGrant(t *testing.T) {
	r := newCreateRig()
	req := order(seat("cat_vip", nikA))
	req.EventID = ""
	_, err := r.create(guestGrant, req)
	wantAppError(t, err, http.StatusBadRequest, "VALIDATION_ERROR")
	r.touchedNothingBeyondTheStatus(t)
}
