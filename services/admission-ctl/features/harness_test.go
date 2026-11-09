package features

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/specsuite"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/postgres"
	redisadapter "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/redis"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

// ---------------------------------------------------------------- clock and metrics

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) set(t time.Time)     { c.mu.Lock(); c.now = t; c.mu.Unlock() }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

type fakeMetrics struct {
	mu     sync.Mutex
	counts map[string]int
	gauges map[string]float64
}

func newMetrics() *fakeMetrics {
	return &fakeMetrics{counts: map[string]int{}, gauges: map[string]float64{}}
}
func (m *fakeMetrics) Inc(n string)              { m.mu.Lock(); m.counts[n]++; m.mu.Unlock() }
func (m *fakeMetrics) Gauge(n string, v float64) { m.mu.Lock(); m.gauges[n] = v; m.mu.Unlock() }
func (m *fakeMetrics) count(n string) int        { m.mu.Lock(); defer m.mu.Unlock(); return m.counts[n] }

// ---------------------------------------------------------------- crash injection

// crashed is panicked by a spy at an injected crash point; the test treats it as the controller dying.
type crashed struct{ at string }

// ---------------------------------------------------------------- spy over the Redis adapter

type call struct {
	op    string // Snapshot, Pending, PendingCount, Advance, Token, PutGrant, AttachGrant, Dequeue, View, SetRate, Close, PublishBroadcast, PublishStatus
	event string
	arg   string
	args  redisscript.Args
}

// callLog is shared by the spy over Redis and the spy over Aurora, so the order between them is visible.
type callLog struct {
	mu    sync.Mutex
	calls []call
}

func (l *callLog) add(c call) { l.mu.Lock(); l.calls = append(l.calls, c); l.mu.Unlock() }
func (l *callLog) snapshot() []call {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]call(nil), l.calls...)
}
func (l *callLog) reset() { l.mu.Lock(); l.calls = nil; l.mu.Unlock() }
func (l *callLog) count(op string) int {
	n := 0
	for _, c := range l.snapshot() {
		if c.op == op {
			n++
		}
	}
	return n
}

type spyQueue struct {
	inner outbound.Queue
	log   *callLog

	crashAfter map[string]int // op -> nth call of that op after which the controller dies
	seen       map[string]int
	forced     *redisscript.Result // when set, Advance returns it and skips Redis
	lastResult redisscript.Result  // what the last Advance returned
	beforeAdv  func()              // runs before Advance (another writer moving the queue)
	slow       time.Duration       // first call of a tick takes this long (TickOverrun)
	slowOnce   atomic.Bool
	inflight   atomic.Int32
	maxFlight  atomic.Int32
	broadcasts [][]byte
	statuses   [][]byte
	mu         sync.Mutex
}

func (s *spyQueue) enter(c call) {
	s.log.add(c)
	n := s.inflight.Add(1)
	for {
		m := s.maxFlight.Load()
		if n <= m || s.maxFlight.CompareAndSwap(m, n) {
			break
		}
	}
	if s.slow > 0 && s.slowOnce.CompareAndSwap(false, true) {
		time.Sleep(s.slow)
	}
}

func (s *spyQueue) leave(op string) {
	s.inflight.Add(-1)
	s.mu.Lock()
	s.seen[op]++
	hit := s.crashAfter[op] > 0 && s.seen[op] == s.crashAfter[op]
	s.mu.Unlock()
	if hit {
		panic(crashed{at: "after " + op})
	}
}

func (s *spyQueue) Snapshot(ctx context.Context, ev string) (int64, int64, error) {
	s.enter(call{op: "Snapshot", event: ev})
	defer s.leave("Snapshot")
	return s.inner.Snapshot(ctx, ev)
}
func (s *spyQueue) Pending(ctx context.Context, ev string, upTo int64) ([]string, error) {
	s.enter(call{op: "Pending", event: ev, arg: fmt.Sprint(upTo)})
	defer s.leave("Pending")
	return s.inner.Pending(ctx, ev, upTo)
}
func (s *spyQueue) PendingCount(ctx context.Context, ev string, upTo int64) (int64, error) {
	s.enter(call{op: "PendingCount", event: ev, arg: fmt.Sprint(upTo)})
	defer s.leave("PendingCount")
	return s.inner.PendingCount(ctx, ev, upTo)
}
func (s *spyQueue) Advance(ctx context.Context, e domain.Event, a redisscript.Args) (redisscript.Result, error) {
	s.enter(call{op: "Advance", event: e.ID, args: a})
	defer s.leave("Advance")
	if s.beforeAdv != nil {
		s.beforeAdv()
	}
	if s.forced != nil {
		s.lastResult = *s.forced
		return *s.forced, nil
	}
	res, err := s.inner.Advance(ctx, e, a)
	s.lastResult = res
	return res, err
}
func (s *spyQueue) Token(ctx context.Context, qt string) (domain.TokenInfo, bool, error) {
	s.enter(call{op: "Token", arg: qt})
	defer s.leave("Token")
	return s.inner.Token(ctx, qt)
}
func (s *spyQueue) PutGrant(ctx context.Context, ev, qt string, b domain.Booking, i domain.TokenInfo, ttl time.Duration) error {
	s.enter(call{op: "PutGrant", event: ev, arg: qt})
	defer s.leave("PutGrant")
	return s.inner.PutGrant(ctx, ev, qt, b, i, ttl)
}
func (s *spyQueue) AttachGrant(ctx context.Context, qt string, b domain.Booking) error {
	s.enter(call{op: "AttachGrant", arg: qt})
	defer s.leave("AttachGrant")
	return s.inner.AttachGrant(ctx, qt, b)
}
func (s *spyQueue) Dequeue(ctx context.Context, ev, qt string) error {
	s.enter(call{op: "Dequeue", event: ev, arg: qt})
	defer s.leave("Dequeue")
	return s.inner.Dequeue(ctx, ev, qt)
}
func (s *spyQueue) View(ctx context.Context, ev string) (domain.EventView, error) {
	s.enter(call{op: "View", event: ev})
	defer s.leave("View")
	return s.inner.View(ctx, ev)
}
func (s *spyQueue) SetRate(ctx context.Context, ev string, r int64) error {
	s.enter(call{op: "SetRate", event: ev, arg: fmt.Sprint(r)})
	defer s.leave("SetRate")
	return s.inner.SetRate(ctx, ev, r)
}
func (s *spyQueue) Close(ctx context.Context, ev string, fullBookedAt int64) error {
	s.enter(call{op: "Close", event: ev})
	defer s.leave("Close")
	return s.inner.Close(ctx, ev, fullBookedAt)
}
func (s *spyQueue) DropGrant(ctx context.Context, gt string) error {
	s.enter(call{op: "DropGrant", arg: gt})
	defer s.leave("DropGrant")
	return s.inner.DropGrant(ctx, gt)
}
func (s *spyQueue) PublishBroadcast(ctx context.Context, p []byte) error {
	s.enter(call{op: "PublishBroadcast", arg: string(p)})
	defer s.leave("PublishBroadcast")
	s.mu.Lock()
	s.broadcasts = append(s.broadcasts, p)
	s.mu.Unlock()
	return s.inner.PublishBroadcast(ctx, p)
}
func (s *spyQueue) PublishStatus(ctx context.Context, p []byte) error {
	s.enter(call{op: "PublishStatus", arg: string(p)})
	defer s.leave("PublishStatus")
	s.mu.Lock()
	s.statuses = append(s.statuses, p)
	s.mu.Unlock()
	return s.inner.PublishStatus(ctx, p)
}

// ---------------------------------------------------------------- spy over Aurora

var errDown = errors.New("connection refused")

type spyBookings struct {
	inner outbound.Bookings
	log   *callLog

	mu          sync.Mutex
	countErr    map[string]error // event -> error ("" key = every event)
	forcedCount *domain.Admission
	insertFails map[string]int // queue_token ("*" = any) -> remaining failures
	failAll     bool
	crashAfter  map[string]int
	seen        map[string]int
	afterInsert func()

	crashAfterLapse bool
}

func (b *spyBookings) CountAdmission(ctx context.Context, ev string) (domain.Admission, error) {
	b.log.add(call{op: "Count", event: ev})
	b.mu.Lock()
	err := b.countErr[ev]
	if err == nil {
		err = b.countErr[""]
	}
	forced := b.forcedCount
	b.mu.Unlock()
	if err != nil {
		return domain.Admission{}, err
	}
	if forced != nil {
		return *forced, nil
	}
	return b.inner.CountAdmission(ctx, ev)
}

func (b *spyBookings) InsertGranted(ctx context.Context, g domain.Grant) (domain.Booking, error) {
	b.log.add(call{op: "Insert", event: g.EventID, arg: g.QueueToken})
	b.mu.Lock()
	fail := b.failAll
	for _, k := range []string{g.QueueToken, "*"} {
		if b.insertFails[k] > 0 {
			b.insertFails[k]--
			fail = true
			break
		}
	}
	b.mu.Unlock()
	if fail {
		return domain.Booking{}, errDown
	}
	out, err := b.inner.InsertGranted(ctx, g)
	if b.afterInsert != nil {
		b.afterInsert()
	}
	b.mu.Lock()
	b.seen["Insert"]++
	hit := b.crashAfter["Insert"] > 0 && b.seen["Insert"] == b.crashAfter["Insert"]
	b.mu.Unlock()
	if hit {
		panic(crashed{at: "after Insert"})
	}
	return out, err
}

func (b *spyBookings) LapseDue(ctx context.Context, now time.Time) ([]domain.Lapsed, error) {
	b.log.add(call{op: "Lapse"})
	out, err := b.inner.LapseDue(ctx, now)
	b.mu.Lock()
	hit := b.crashAfterLapse
	b.mu.Unlock()
	if hit && len(out) > 0 {
		panic(crashed{at: "after the lapse UPDATE"})
	}
	return out, err
}

// ---------------------------------------------------------------- bookings backends (memory for @unit, Postgres for @integration)

type rowView struct {
	ID, EventID, QueueToken, Account, Status, GrantToken string
	GrantedAt, ExpiresAt                                 time.Time
}

type bookingsBackend interface {
	outbound.Bookings
	rows(ctx context.Context, eventID string) ([]rowView, error)
	seedInside(ctx context.Context, eventID string, n int, status string) error
	moveOne(ctx context.Context, eventID, from, to string) error
	reset(ctx context.Context) error
	setDeadline(ctx context.Context, bookingID string, at time.Time) error
	setStatus(ctx context.Context, bookingID, status string) error
}

var activeStatuses = map[string]bool{"GRANTED": true, "SELECTING": true, "HELD": true, "AWAITING_PAYMENT": true, "PENDING_CANCELLATION": true}

type memBookings struct {
	mu   sync.Mutex
	list []*rowView
	seq  int
}

func (m *memBookings) CountAdmission(_ context.Context, ev string) (domain.Admission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var a domain.Admission
	for _, r := range m.list {
		if r.EventID != ev || !activeStatuses[r.Status] {
			continue
		}
		a.Open++
		if r.Status == "GRANTED" || r.Status == "SELECTING" {
			a.Browsing++
		}
	}
	return a, nil
}

func (m *memBookings) InsertGranted(_ context.Context, g domain.Grant) (domain.Booking, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.list {
		if r.EventID == g.EventID && r.QueueToken == g.QueueToken {
			return domain.Booking{ID: r.ID, GrantToken: r.GrantToken, GrantedAt: r.GrantedAt, ExpiresAt: r.ExpiresAt, Status: r.Status}, nil
		}
	}
	m.list = append(m.list, &rowView{ID: g.BookingID, EventID: g.EventID, QueueToken: g.QueueToken, Account: g.AccountID,
		Status: "GRANTED", GrantToken: g.GrantToken, GrantedAt: g.GrantedAt, ExpiresAt: g.ExpiresAt})
	return domain.Booking{ID: g.BookingID, GrantToken: g.GrantToken, GrantedAt: g.GrantedAt, ExpiresAt: g.ExpiresAt, Status: "GRANTED"}, nil
}

func (m *memBookings) rows(_ context.Context, ev string) ([]rowView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []rowView
	for _, r := range m.list {
		if r.EventID == ev {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (m *memBookings) seedInside(_ context.Context, ev string, n int, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := 0; i < n; i++ {
		m.seq++
		m.list = append(m.list, &rowView{ID: fmt.Sprintf("in-%d", m.seq), EventID: ev, QueueToken: fmt.Sprintf("in-q%d", m.seq), Status: status, GrantToken: fmt.Sprintf("in-g%d", m.seq)})
	}
	return nil
}

func (m *memBookings) moveOne(_ context.Context, ev, from, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.list {
		if r.EventID == ev && r.Status == from {
			r.Status = to
			return nil
		}
	}
	return fmt.Errorf("no %s booking to move", from)
}

func (m *memBookings) LapseDue(_ context.Context, now time.Time) ([]domain.Lapsed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Lapsed
	for _, r := range m.list {
		if r.Status == "GRANTED" && !r.ExpiresAt.IsZero() && !r.ExpiresAt.After(now) {
			r.Status = "LAPSED"
			out = append(out, domain.Lapsed{BookingID: r.ID, EventID: r.EventID, QueueToken: r.QueueToken, GrantToken: r.GrantToken})
		}
	}
	return out, nil
}

func (m *memBookings) setDeadline(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.list {
		if r.ID == id {
			r.ExpiresAt = at
		}
	}
	return nil
}

func (m *memBookings) setStatus(_ context.Context, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.list {
		if r.ID == id {
			r.Status = status
		}
	}
	return nil
}

func (m *memBookings) reset(context.Context) error {
	m.mu.Lock()
	m.list = nil
	m.mu.Unlock()
	return nil
}

type pgBookings struct {
	*postgres.Bookings
	db *testDB
}

func (p *pgBookings) rows(ctx context.Context, ev string) ([]rowView, error) {
	rs, err := p.db.admin.Query(ctx, `SELECT id, event_id, COALESCE(queue_token,''), COALESCE(account_id,''), status,
		COALESCE(grant_token,''), COALESCE(granted_at, 'epoch'), COALESCE(booking_expires_at, 'epoch') FROM bookings WHERE event_id=$1 ORDER BY created_at, id`, ev)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []rowView
	for rs.Next() {
		var r rowView
		if err := rs.Scan(&r.ID, &r.EventID, &r.QueueToken, &r.Account, &r.Status, &r.GrantToken, &r.GrantedAt, &r.ExpiresAt); err != nil {
			return nil, err
		}
		r.GrantedAt, r.ExpiresAt = r.GrantedAt.UTC(), r.ExpiresAt.UTC()
		out = append(out, r)
	}
	return out, rs.Err()
}

func (p *pgBookings) seedInside(ctx context.Context, ev string, n int, status string) error {
	if _, err := p.db.admin.Exec(ctx, `INSERT INTO events (id, name, event_date) VALUES ($1,$1,'2026-12-01') ON CONFLICT DO NOTHING`, ev); err != nil {
		return err
	}
	_, err := p.db.admin.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, contact_email, terms_accepted_at, booking_expires_at)
		SELECT 'in-' || $1 || '-' || i, $1, 'in-q-' || $1 || '-' || i || '-' || $2, $2, 'in-g-' || $1 || '-' || i || '-' || $2, 'a@example.com', now(), timestamptz '2099-01-01'
		FROM generate_series(1, $3) AS i`, ev, status, n)
	return err
}

func (p *pgBookings) moveOne(ctx context.Context, ev, from, to string) error {
	tag, err := p.db.admin.Exec(ctx, `UPDATE bookings SET status=$3, contact_email='a@example.com', terms_accepted_at=now()
		WHERE id = (SELECT id FROM bookings WHERE event_id=$1 AND status=$2 LIMIT 1)`, ev, from, to)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("no %s booking to move", from)
	}
	return err
}

func (p *pgBookings) setDeadline(ctx context.Context, id string, at time.Time) error {
	_, err := p.db.admin.Exec(ctx, `UPDATE bookings SET booking_expires_at=$2 WHERE id=$1`, id, at)
	return err
}

func (p *pgBookings) setStatus(ctx context.Context, id, status string) error {
	_, err := p.db.admin.Exec(ctx, `UPDATE bookings SET status=$2, contact_email=COALESCE(contact_email,'a@example.com'), terms_accepted_at=COALESCE(terms_accepted_at, now()) WHERE id=$1`, id, status)
	return err
}

func (p *pgBookings) reset(ctx context.Context) error {
	_, err := p.db.admin.Exec(ctx, "TRUNCATE bookings CASCADE")
	return err
}

// ---------------------------------------------------------------- the world of one tick scenario

type customer struct {
	name, token, account, device string
	ticket                       int64
}

type tickWorld struct {
	ctx   context.Context
	unit  bool
	nonce string

	mr      *miniredis.Miniredis
	rdb     *goredis.Client
	bks     bookingsBackend
	spyB    *spyBookings
	spyQ    *spyQueue
	log     *callLog
	clock   *fakeClock
	metrics *fakeMetrics
	cfg     usecase.Config
	tick    *usecase.Tick

	events    map[string]domain.Event // by spec name ("E1", "E2")
	customers map[string]*customer
	order     []string
	issued    map[string]int64
	tokens    []string // every queue token created, for cleanup
	grantKeys []string

	crashErr error
	lastErr  error
	// scratch for individual scenarios
	scratch map[string]any
}

func newTickWorld(unit bool) *tickWorld {
	return &tickWorld{ctx: context.Background(), unit: unit, nonce: fmt.Sprintf("%d", time.Now().UnixNano()),
		events: map[string]domain.Event{}, customers: map[string]*customer{}, issued: map[string]int64{}, scratch: map[string]any{}}
}

// evID maps a spec name like "E1" to the real event id: unique per scenario on the shared Redis cluster.
func (w *tickWorld) evID(name string) string {
	if w.unit {
		return name
	}
	return name + "-" + w.nonce
}

func (w *tickWorld) setup() error {
	w.log = &callLog{}
	w.clock = &fakeClock{now: time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)}
	w.metrics = newMetrics()
	w.cfg = usecase.DefaultConfig()
	if w.unit {
		w.mr = miniredis.NewMiniRedis()
		if err := w.mr.Start(); err != nil {
			return err
		}
		w.rdb = goredis.NewClient(&goredis.Options{Addr: w.mr.Addr()})
		w.bks = &memBookings{}
	} else {
		w.rdb = goredis.NewClient(&goredis.Options{Addr: specsuite.RedisAddr()})
		db, err := sharedDB(w.ctx)
		if err != nil {
			return err
		}
		pg := &pgBookings{Bookings: postgres.NewBookings(db.writer), db: db}
		if err := pg.reset(w.ctx); err != nil {
			return err
		}
		w.bks = pg
	}
	w.newController(true)
	return nil
}

// newController builds spies and a fresh Tick: the same Redis and Aurora, empty caches (an ECS task restart).
func (w *tickWorld) newController(firstTime bool) {
	w.spyB = &spyBookings{inner: w.bks, log: w.log, countErr: map[string]error{}, insertFails: map[string]int{}, crashAfter: map[string]int{}, seen: map[string]int{}}
	w.spyQ = &spyQueue{inner: redisadapter.NewQueue(w.rdb), log: w.log, crashAfter: map[string]int{}, seen: map[string]int{}}
	w.buildTick()
}

func (w *tickWorld) buildTick() {
	w.cfg.Events = nil
	for _, name := range w.eventNames() {
		w.cfg.Events = append(w.cfg.Events, w.events[name])
	}
	w.tick = usecase.NewTick(w.cfg, w.spyB, w.spyQ, w.clock, w.metrics)
}

func (w *tickWorld) eventNames() []string {
	var names []string
	for n := range w.events {
		names = append(names, n)
	}
	// stable: E1 before E2
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

func (w *tickWorld) cleanup() {
	if w.rdb == nil {
		return
	}
	if !w.unit {
		var keys []string
		for _, ev := range w.events {
			e, err := rediskeys.NewEvent(ev.ID)
			if err != nil {
				continue
			}
			keys = append(keys, e.QueueHash(), e.ByTicket())
			for _, c := range ev.Categories {
				keys = append(keys, e.Stock(c))
			}
		}
		for _, t := range w.tokens {
			keys = append(keys, rediskeys.Token(t))
		}
		keys = append(keys, w.grantKeys...)
		for _, k := range keys {
			_ = w.rdb.Del(w.ctx, k).Err()
		}
	}
	_ = w.rdb.Close()
	if w.mr != nil {
		w.mr.Close()
	}
}

// ---------------------------------------------------------------- seeding helpers

func (w *tickWorld) addEvent(name, state string, stock int64) error {
	ev := domain.Event{ID: w.evID(name), Categories: []string{"VIP", "CAT1"}}
	w.events[name] = ev
	e, err := rediskeys.NewEvent(ev.ID)
	if err != nil {
		return err
	}
	if err := w.rdb.HSet(w.ctx, e.QueueHash(), rediskeys.FieldState, state, rediskeys.FieldNowServing, 0, rediskeys.FieldIssued, 0).Err(); err != nil {
		return err
	}
	if err := w.setStock(name, stock); err != nil {
		return err
	}
	w.buildTick()
	return nil
}

func (w *tickWorld) key(name string) rediskeys.Event {
	e, err := rediskeys.NewEvent(w.evID(name))
	if err != nil {
		panic(err)
	}
	return e
}

func (w *tickWorld) setStock(name string, total int64) error {
	e := w.key(name)
	if err := w.rdb.Set(w.ctx, e.Stock("VIP"), total, 0).Err(); err != nil {
		return err
	}
	return w.rdb.Set(w.ctx, e.Stock("CAT1"), 0, 0).Err()
}

// join adds a waiting customer exactly like the join service does: a token hash, a place in by_ticket, issued + 1.
func (w *tickWorld) join(eventName, name string, guest bool) (*customer, error) {
	e := w.key(eventName)
	n, err := w.rdb.HIncrBy(w.ctx, e.QueueHash(), rediskeys.FieldIssued, 1).Result()
	if err != nil {
		return nil, err
	}
	c := &customer{name: name, token: fmt.Sprintf("qt-%s-%s", name, w.nonce), ticket: n}
	fields := map[string]any{rediskeys.FieldTicketNo: n, "event_id": w.evID(eventName)}
	if guest {
		c.device = "dev-hash-" + name
		fields[rediskeys.FieldDeviceHash] = c.device
	} else {
		c.account = "acc-" + name + "-" + w.nonce
		if pg, ok := w.bks.(*pgBookings); ok { // bookings.account_id references accounts
			if _, err := pg.db.admin.Exec(w.ctx, `INSERT INTO accounts (id, email) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				c.account, c.account+"@example.com"); err != nil {
				return nil, err
			}
		}
		fields[rediskeys.FieldAccountID] = c.account
	}
	if err := w.rdb.HSet(w.ctx, rediskeys.Token(c.token), fields).Err(); err != nil {
		return nil, err
	}
	if err := w.rdb.Expire(w.ctx, rediskeys.Token(c.token), 24*time.Hour).Err(); err != nil {
		return nil, err
	}
	if err := w.rdb.ZAdd(w.ctx, e.ByTicket(), goredis.Z{Score: float64(n), Member: c.token}).Err(); err != nil {
		return nil, err
	}
	w.customers[name] = c
	w.order = append(w.order, name)
	w.tokens = append(w.tokens, c.token)
	return c, nil
}

func (w *tickWorld) setNowServing(name string, n int64) error {
	return w.rdb.HSet(w.ctx, w.key(name).QueueHash(), rediskeys.FieldNowServing, n).Err()
}

func (w *tickWorld) field(name, f string) (string, error) {
	v, err := w.rdb.HGet(w.ctx, w.key(name).QueueHash(), f).Result()
	if errors.Is(err, goredis.Nil) {
		return "", nil
	}
	return v, err
}

// runTick runs one tick for every event; a crash point turns into w.crashErr instead of a test panic.
func (w *tickWorld) runTick() {
	w.crashErr, w.lastErr = nil, nil
	func() {
		defer func() {
			if r := recover(); r != nil {
				c, ok := r.(crashed)
				if !ok {
					panic(r)
				}
				w.crashErr = fmt.Errorf("controller died %s", c.at)
			}
		}()
		w.tick.Sweep(w.ctx)
		for _, ev := range w.cfg.Events {
			if err := w.tick.RunEvent(w.ctx, ev); err != nil {
				w.lastErr = err
			}
		}
	}()
}

func (w *tickWorld) primary() domain.Event { return w.events["E1"] }

func (w *tickWorld) wantRows(name string) ([]rowView, error) { return w.bks.rows(w.ctx, w.evID(name)) }

func advArgs(max, seen, now int64) redisscript.Args {
	return redisscript.Args{Max: max, Open: 0, Browsing: 0, NowServing: seen, Now: now}
}
