package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// ---- a fake pgx transaction: what Place needs, nothing else (anything else panics through the nil embed)

type execResult struct {
	tag pgconn.CommandTag
	err error
}

// execBatch answers the statements of one SendBatch in order, through Exec.
type execBatch struct {
	results []execResult
	next    int
}

func (b *execBatch) Exec() (pgconn.CommandTag, error) {
	r := b.results[b.next]
	b.next++
	return r.tag, r.err
}
func (b *execBatch) Query() (pgx.Rows, error) { return nil, errors.New("not faked") }
func (b *execBatch) QueryRow() pgx.Row        { return fakeRow{err: errors.New("not faked")} }
func (b *execBatch) Close() error             { return nil }

type fakeTx struct {
	pgx.Tx
	lock      pgx.Row
	batch     *execBatch
	commitErr error
	calls     []call
	batched   []*pgx.QueuedQuery
	commits   int
	rollbacks int
}

func (f *fakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.calls = append(f.calls, call{sql, args})
	return f.lock
}

func (f *fakeTx) SendBatch(_ context.Context, b *pgx.Batch) pgx.BatchResults {
	f.batched = b.QueuedQueries
	return f.batch
}

func (f *fakeTx) Commit(context.Context) error   { f.commits++; return f.commitErr }
func (f *fakeTx) Rollback(context.Context) error { f.rollbacks++; return nil }

func lockRow(status string, pastDeadline bool) fakeRow {
	return fakeRow{vals: []any{status, pastDeadline}}
}

func okBatch(statements int) *execBatch {
	rs := make([]execResult, statements)
	rs[0] = execResult{tag: pgconn.NewCommandTag("UPDATE 1")}
	for i := 1; i < statements; i++ {
		rs[i] = execResult{tag: pgconn.NewCommandTag("INSERT 0 1")}
	}
	return &execBatch{results: rs}
}

func placement() domain.Placement {
	return domain.Placement{
		BookingID: "RB-1", GrantToken: "gt_RB-1", EventID: "evt_a", ContactEmail: "buyer@example.com",
		Seats: []domain.Seat{
			{ID: "BI-1", CategoryID: "cat_vip", CategoryName: "VIP", UnitPrice: 1500000, NIKHmac: "hmac-a", NIKMasked: "3171**********22"},
			{ID: "BI-2", CategoryID: "cat_fest", CategoryName: "Festival", UnitPrice: 850000, NIKHmac: "hmac-b", NIKMasked: "3171**********33"},
		},
	}
}

var (
	held = func(context.Context) (domain.HoldAnswer, error) {
		return domain.HoldAnswer{Code: domain.HoldHeld, Index: -1}, nil
	}
	refused = func(context.Context) (domain.HoldAnswer, error) {
		return domain.HoldAnswer{Code: domain.HoldNoStock, Index: 1}, nil
	}
	failed = func(context.Context) (domain.HoldAnswer, error) { return domain.HoldAnswer{}, errDB }
	never  = func(t *testing.T) outbound.HoldFunc {
		return func(context.Context) (domain.HoldAnswer, error) {
			t.Fatal("the hold must not run")
			return domain.HoldAnswer{}, nil
		}
	}
)

func placeWith(tx *fakeTx, hold outbound.HoldFunc) (domain.PlaceResult, error) {
	return newStore(&fakeDB{tx: tx}).Place(ctx, placement(), hold)
}

// ---- Place

func TestAPlacedBookingWritesTheHeldRowItsSeatsAndItsNIKClaimsInOneBatchThenCommits(t *testing.T) {
	tx := &fakeTx{lock: lockRow("SELECTING", false), batch: okBatch(5)}
	res, err := placeWith(tx, held)
	if err != nil || res.Outcome != domain.Placed {
		t.Fatalf("result = %+v (%v), want Placed", res, err)
	}
	if tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("commits=%d rollbacks=%d, want one commit", tx.commits, tx.rollbacks)
	}
	if len(tx.calls) != 1 || !strings.Contains(tx.calls[0].sql, "FOR UPDATE") || !reflect.DeepEqual(tx.calls[0].args, []any{"RB-1", "gt_RB-1"}) {
		t.Fatalf("lock = %+v, want the row locked by id AND grant token", tx.calls)
	}
	q := tx.batched
	if len(q) != 5 {
		t.Fatalf("%d statements, want the update, 2 items and 2 claims", len(q))
	}
	if !strings.Contains(q[0].SQL, "status = 'HELD'") || !strings.Contains(q[0].SQL, "held_at = now()") || !strings.Contains(q[0].SQL, "status = 'SELECTING'") ||
		!reflect.DeepEqual(q[0].Arguments, []any{"RB-1", int64(600), "buyer@example.com"}) {
		t.Fatalf("update = %s %v", q[0].SQL, q[0].Arguments)
	}
	if !strings.Contains(q[1].SQL, "INSERT INTO booking_items") || !strings.Contains(q[1].SQL, "clock_timestamp()") ||
		!reflect.DeepEqual(q[1].Arguments, []any{"BI-1", "RB-1", "evt_a", "cat_vip", "VIP", int64(1500000), "hmac-a", "3171**********22"}) {
		t.Fatalf("first item = %s %v", q[1].SQL, q[1].Arguments)
	}
	if !reflect.DeepEqual(q[2].Arguments, []any{"BI-2", "RB-1", "evt_a", "cat_fest", "Festival", int64(850000), "hmac-b", "3171**********33"}) {
		t.Fatalf("second item = %v", q[2].Arguments)
	}
	if !strings.Contains(q[3].SQL, "INSERT INTO nik_claims") || !reflect.DeepEqual(q[3].Arguments, []any{"evt_a", "hmac-a", "RB-1"}) ||
		!reflect.DeepEqual(q[4].Arguments, []any{"evt_a", "hmac-b", "RB-1"}) {
		t.Fatalf("claims = %v / %v", q[3].Arguments, q[4].Arguments)
	}
}

// What the lock sees decides before anything is held: the same answers as the read before the body.
func TestWhatTheLockSeesIsAnsweredBeforeTheHoldRuns(t *testing.T) {
	cases := []struct {
		name string
		lock pgx.Row
		want domain.PlaceOutcome
	}{
		{"no row", fakeRow{err: pgx.ErrNoRows}, domain.Gone},
		{"held", lockRow("HELD", false), domain.AlreadyPlaced},
		{"awaiting payment", lockRow("AWAITING_PAYMENT", false), domain.AlreadyPlaced},
		{"pending cancellation", lockRow("PENDING_CANCELLATION", true), domain.AlreadyPlaced},
		{"confirmed", lockRow("CONFIRMED", true), domain.AlreadyPlaced},
		{"granted in time", lockRow("GRANTED", false), domain.NotOpened},
		{"granted past the deadline", lockRow("GRANTED", true), domain.TimeOver},
		{"selecting past the deadline", lockRow("SELECTING", true), domain.TimeOver},
		{"lapsed", lockRow("LAPSED", true), domain.Gone},
		{"expired", lockRow("EXPIRED", true), domain.Gone},
		{"abandoned", lockRow("ABANDONED", false), domain.Gone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &fakeTx{lock: tc.lock}
			res, err := placeWith(tx, never(t))
			if err != nil || res.Outcome != tc.want {
				t.Fatalf("result = %+v (%v), want %v", res, err, tc.want)
			}
			if tx.batched != nil || tx.commits != 0 || tx.rollbacks != 1 {
				t.Fatalf("batched=%v commits=%d rollbacks=%d, want nothing written and the lock released", tx.batched, tx.commits, tx.rollbacks)
			}
		})
	}
}

// A refused hold changes nothing: the transaction commits as it is and the booking stays SELECTING.
func TestARefusedHoldCommitsNothingAndIsReported(t *testing.T) {
	tx := &fakeTx{lock: lockRow("SELECTING", false)}
	res, err := placeWith(tx, refused)
	if err != nil || res.Outcome != domain.Refused || res.Hold.Code != domain.HoldNoStock || res.Hold.Index != 1 {
		t.Fatalf("result = %+v (%v)", res, err)
	}
	if tx.batched != nil || tx.commits != 1 {
		t.Fatalf("batched=%v commits=%d, want no writes and a plain commit", tx.batched, tx.commits)
	}
}

func TestAHoldWhoseAnswerIsUnknownRollsBackAndReturnsTheError(t *testing.T) {
	tx := &fakeTx{lock: lockRow("SELECTING", false)}
	if _, err := placeWith(tx, failed); !errors.Is(err, errDB) {
		t.Fatalf("err = %v, want the hold's error", err)
	}
	if tx.batched != nil || tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("batched=%v commits=%d rollbacks=%d", tx.batched, tx.commits, tx.rollbacks)
	}
}

// nik_claims is the rule when Redis missed a NIK: the unique violation rolls back and names the seat.
func TestANIKClaimConflictRollsBackAndNamesTheSeat(t *testing.T) {
	conflict := &pgconn.PgError{Code: "23505", ConstraintName: "nik_claims_pkey"}
	b := okBatch(5)
	b.results[4].err = conflict // the claim of the second seat
	tx := &fakeTx{lock: lockRow("SELECTING", false), batch: b}
	res, err := placeWith(tx, held)
	if err != nil || res.Outcome != domain.ClaimConflict || res.ConflictIndex != 1 {
		t.Fatalf("result = %+v (%v), want ClaimConflict at seat 1", res, err)
	}
	if tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d, want a rollback", tx.commits, tx.rollbacks)
	}
}

func TestOtherWriteFailuresRollBackAndAreErrors(t *testing.T) {
	other := &pgconn.PgError{Code: "23505", ConstraintName: "booking_items_booking_nik_key"} // not nik_claims
	cases := map[string]func(*execBatch){
		"the update fails":          func(b *execBatch) { b.results[0].err = errDB },
		"the update matches no row": func(b *execBatch) { b.results[0].tag = pgconn.NewCommandTag("UPDATE 0") },
		"an item fails":             func(b *execBatch) { b.results[1].err = errDB },
		"another unique violation":  func(b *execBatch) { b.results[2].err = other },
		"a claim fails otherwise":   func(b *execBatch) { b.results[3].err = errDB },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := okBatch(5)
			mutate(b)
			tx := &fakeTx{lock: lockRow("SELECTING", false), batch: b}
			res, err := placeWith(tx, held)
			if err == nil || res.Outcome != 0 {
				t.Fatalf("result = %+v err = %v, want an error", res, err)
			}
			if tx.commits != 0 || tx.rollbacks != 1 {
				t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
			}
			if errors.Is(err, outbound.ErrCommitUnknown) {
				t.Fatal("a failure before the commit is not an unknown commit")
			}
		})
	}
}

func TestACommitThatFailsIsAnUnknownResultNotAPlainError(t *testing.T) {
	tx := &fakeTx{lock: lockRow("SELECTING", false), batch: okBatch(5), commitErr: errDB}
	_, err := placeWith(tx, held)
	if !errors.Is(err, outbound.ErrCommitUnknown) || !errors.Is(err, errDB) {
		t.Fatalf("err = %v, want ErrCommitUnknown wrapping the cause", err)
	}
}

func TestPlaceReportsBeginAndLockFailures(t *testing.T) {
	if _, err := newStore(&fakeDB{beginErr: errDB}).Place(ctx, placement(), never(t)); !errors.Is(err, errDB) {
		t.Fatalf("begin: %v", err)
	}
	tx := &fakeTx{lock: fakeRow{err: errDB}}
	if _, err := placeWith(tx, never(t)); !errors.Is(err, errDB) || tx.rollbacks != 1 {
		t.Fatalf("lock: err=%v rollbacks=%d", err, tx.rollbacks)
	}
	tx = &fakeTx{lock: lockRow("SOLD_OUT", false)}
	if _, err := placeWith(tx, never(t)); err == nil || tx.rollbacks != 1 {
		t.Fatalf("unknown status: err=%v rollbacks=%d, want an error, never a default", err, tx.rollbacks)
	}
}

// ---- Status

func TestTheStatusReadReturnsTheStatusAndWhetherTheDeadlinePassed(t *testing.T) {
	f := &fakeDB{rows: []pgx.Row{lockRow("SELECTING", true)}}
	st, found, err := newStore(f).Status(ctx, "RB-1", "gt_1")
	if err != nil || !found || st != (domain.BookingStatus{Status: bookingstate.Selecting, PastDeadline: true}) {
		t.Fatalf("status = %+v found=%v err=%v", st, found, err)
	}
	if len(f.calls) != 1 || strings.Contains(f.calls[0].sql, "FOR UPDATE") || !reflect.DeepEqual(f.calls[0].args, []any{"RB-1", "gt_1"}) {
		t.Fatalf("call = %+v, want one plain read bound to id AND grant token", f.calls)
	}
}

func TestTheStatusReadFindsNothingOrFails(t *testing.T) {
	if _, found, err := newStore(&fakeDB{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}}}).Status(ctx, "x", "y"); err != nil || found {
		t.Fatalf("found=%v err=%v, want not found", found, err)
	}
	if _, _, err := newStore(&fakeDB{rows: []pgx.Row{fakeRow{err: errDB}}}).Status(ctx, "x", "y"); !errors.Is(err, errDB) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := newStore(&fakeDB{rows: []pgx.Row{lockRow("SOLD_OUT", false)}}).Status(ctx, "x", "y"); err == nil {
		t.Fatal("an unknown status must be an error, never a default")
	}
}

// ---- Categories

func TestCategoriesComeBackInDisplayOrder(t *testing.T) {
	f := &fakeDB{query: &fakeRows{data: [][]any{
		{"cat_vip", "VIP", "Dekat", int64(1500000), 2},
		{"cat_fest", "Festival", "", int64(850000), 3},
	}}}
	cats, err := newStore(f).Categories(ctx, "evt_a")
	want := []domain.Category{
		{ID: "cat_vip", Name: "VIP", Description: "Dekat", Price: 1500000, MaxPerOrder: 2},
		{ID: "cat_fest", Name: "Festival", Price: 850000, MaxPerOrder: 3},
	}
	if err != nil || !reflect.DeepEqual(cats, want) {
		t.Fatalf("categories = %+v (%v)", cats, err)
	}
	if !strings.Contains(f.calls[0].sql, "ORDER BY sort_order, id") || !reflect.DeepEqual(f.calls[0].args, []any{"evt_a"}) {
		t.Fatalf("call = %+v", f.calls[0])
	}
}

func TestCategoriesReportEveryDatabaseError(t *testing.T) {
	cases := map[string]*fakeDB{
		"query":  {queryErr: errDB},
		"scan":   {query: &fakeRows{data: [][]any{{"c", "n", "", int64(1), 1}}, scanErr: errDB}},
		"cursor": {query: &fakeRows{err: errDB}},
	}
	for name, f := range cases {
		if _, err := newStore(f).Categories(ctx, "evt_a"); !errors.Is(err, errDB) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// ---- Identities

func TestIdentitiesReadTheOwnVerifiedNIKAndTheSavedOnesInOneBatch(t *testing.T) {
	f := &fakeDB{batch: &fakeBatch{
		first:  fakeRow{vals: []any{"hmac-own", "3171**********22"}},
		second: &fakeRows{data: [][]any{{"sn_1", "hmac-s1", "3171**********33"}, {"sn_2", "hmac-s2", "3171**********44"}}},
	}}
	ids, err := newStore(f).Identities(ctx, "acc_1", []string{"sn_1", "sn_2", "sn_gone"})
	want := domain.Identities{
		Own: &domain.Identity{NIKHmac: "hmac-own", NIKMasked: "3171**********22"},
		Saved: map[string]domain.Identity{
			"sn_1": {NIKHmac: "hmac-s1", NIKMasked: "3171**********33"},
			"sn_2": {NIKHmac: "hmac-s2", NIKMasked: "3171**********44"},
		},
	}
	if err != nil || !reflect.DeepEqual(ids, want) {
		t.Fatalf("identities = %+v (%v)", ids, err)
	}
	if len(f.batched) != 2 || !strings.Contains(f.batched[0].SQL, "nik_verified_at IS NOT NULL") ||
		!strings.Contains(f.batched[1].SQL, "account_id = $1") || !reflect.DeepEqual(f.batched[1].Arguments, []any{"acc_1", []string{"sn_1", "sn_2", "sn_gone"}}) {
		t.Fatalf("batch = %+v, want both reads bound to the grant's account", f.batched)
	}
}

func TestAnAccountWithoutAVerifiedNIKHasNoOwnIdentityAndNoSavedIdsSkipsThatRead(t *testing.T) {
	f := &fakeDB{batch: &fakeBatch{first: fakeRow{err: pgx.ErrNoRows}}}
	ids, err := newStore(f).Identities(ctx, "acc_1", nil)
	if err != nil || ids.Own != nil || len(ids.Saved) != 0 || len(f.batched) != 1 {
		t.Fatalf("identities = %+v statements=%d (%v), want no own NIK and one statement", ids, len(f.batched), err)
	}
}

func TestIdentitiesReportEveryDatabaseError(t *testing.T) {
	cases := map[string]*fakeBatch{
		"own":          {first: fakeRow{err: errDB}, second: &fakeRows{}},
		"saved query":  {first: fakeRow{err: pgx.ErrNoRows}, queryErr: errDB},
		"saved scan":   {first: fakeRow{err: pgx.ErrNoRows}, second: &fakeRows{data: [][]any{{"a", "b", "c"}}, scanErr: errDB}},
		"saved cursor": {first: fakeRow{err: pgx.ErrNoRows}, second: &fakeRows{err: errDB}},
	}
	for name, b := range cases {
		if _, err := newStore(&fakeDB{batch: b}).Identities(ctx, "acc_1", []string{"sn_1"}); !errors.Is(err, errDB) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

var _ = time.Second

// retryable is an error pgconn.SafeToRetry recognises: the request never reached the server.
type retryable struct{}

func (retryable) Error() string     { return "never sent" }
func (retryable) SafeToRetry() bool { return true }

// A commit that is known not to have happened is an ordinary failure: the hold is given back. Treating it as unknown
// would keep the customer's own NIKs locked, and the retry would be refused with NIK_ALREADY_USED.
func TestACommitKnownNotToHaveHappenedIsAPlainErrorSoTheHoldIsGivenBack(t *testing.T) {
	for name, cause := range map[string]error{
		"the transaction was rolled back by the server": pgx.ErrTxCommitRollback,
		"the commit never left the client":              retryable{},
	} {
		t.Run(name, func(t *testing.T) {
			tx := &fakeTx{lock: lockRow("SELECTING", false), batch: okBatch(5), commitErr: cause}
			_, err := placeWith(tx, held)
			if err == nil || errors.Is(err, outbound.ErrCommitUnknown) {
				t.Fatalf("err = %v, want a plain error that is not ErrCommitUnknown", err)
			}
		})
	}
}
