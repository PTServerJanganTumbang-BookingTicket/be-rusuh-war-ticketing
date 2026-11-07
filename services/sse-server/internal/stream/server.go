package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

// Config holds the stream's tunables (api-contracts §3.2).
type Config struct {
	Keepalive     time.Duration   // comment frame interval (15 s)
	StaleAfter    time.Duration   // no broadcast for this long → stale (30 s)
	StaleCheck    time.Duration   // how often freshness is checked (1 s)
	BackoffMax    time.Duration   // cap of the resubscribe backoff (5 s)
	DrainDeadline time.Duration   // SIGTERM → hard close (20 s)
	RetryMin      time.Duration   // reconnect hint range (1 s .. 15 s)
	RetryMax      time.Duration   //
	ConnBuffer    int             // queued frames per connection before it is dropped
	IPLimit       ratelimit.Limit // rl:sse:{ip} (5 / s)
}

// DefaultConfig holds the contract's values.
func DefaultConfig() Config {
	return Config{
		Keepalive: 15 * time.Second, StaleAfter: 30 * time.Second, StaleCheck: time.Second,
		BackoffMax: 5 * time.Second, DrainDeadline: 20 * time.Second,
		RetryMin: time.Second, RetryMax: 15 * time.Second, ConnBuffer: 256,
		IPLimit: ratelimit.Limit{Capacity: 5, Refill: 5, Per: time.Second},
	}
}

// Message is one pub/sub message.
type Message struct {
	Channel string
	Payload []byte
}

// Subscription is a live pub/sub subscription; Messages closes when it drops.
type Subscription interface {
	Messages() <-chan Message
	Close() error
}

// Source opens subscriptions to sse-broadcast and sse-status. Classic pub/sub
// today; moving to sharded pub/sub changes only this adapter (ADR-006 §9).
type Source interface {
	Subscribe(ctx context.Context) (Subscription, error)
}

// Store is the Redis side of connect: gates and the snapshot.
type Store interface {
	// State reads queue:{ev}:state. found is false when the key does not exist.
	State(ctx context.Context, ev rediskeys.Event) (state string, found bool, err error)
	// Numbers reads now_serving and rate_per_min from queue:{ev} (0 when absent).
	Numbers(ctx context.Context, ev rediskeys.Event) (nowServing, ratePerMin int64, err error)
	// TokenEvent returns the event a queue_token belongs to.
	TokenEvent(ctx context.Context, queueToken string) (eventID string, found bool, err error)
}

// Limiter is pkg/ratelimit's Limiter.
type Limiter interface {
	Allow(ctx context.Context, key string, lim ratelimit.Limit) (ratelimit.Result, error)
}

// Metrics records operational metrics.
type Metrics interface {
	Incr(name string)
	Gauge(name, event string, value float64)
}

// Deps are the server's collaborators.
type Deps struct {
	Store   Store
	Limiter Limiter
	Source  Source
	Metrics Metrics
	Clock   Clock
	Rand    func(n int64) int64 // uniform in [0, n); jitter for retry hints and backoff
}

// Metric names.
const (
	MetricBadMessage     = "SSEBadMessage"
	MetricBackpressure   = "SSEWriteBackpressure"
	MetricMessageBuilt   = "SSEMessageBuilt"
	MetricRateLimitOpen  = "RateLimitFailOpen"
	MetricConnections    = "SSEConnections"
	MetricHeartbeatAge   = "CtlHeartbeatAge"
	MetricResubscribeSec = "SSEResubscribeDelaySeconds"
)

const (
	staleNoUpdates = "no_admission_updates"
	staleRedisDown = "redis_disconnected"
)

// conn is one customer's stream. The fan-out never blocks on it: a full
// buffer drops the connection (a phone that stopped reading must not slow the rest).
type conn struct {
	ch   chan []byte // nil frame = end the response
	done chan struct{}
	once sync.Once
}

func (c *conn) send(b []byte) bool {
	select {
	case c.ch <- b:
		return true
	default:
		return false
	}
}

func (c *conn) kill() { c.once.Do(func() { close(c.done) }) }

// room is the set of connections of one event on this server.
type room struct {
	id        string
	conns     map[*conn]struct{}
	state     string
	lastNS    int64
	lastFresh time.Time
	stale     bool
	reason    string
}

// Server is the stream server.
type Server struct {
	cfg Config
	d   Deps

	mu       sync.Mutex
	rooms    map[string]*room
	draining bool
	idle     chan struct{}

	connected     atomic.Bool
	hadDisconnect atomic.Bool
}

// NewServer builds a server.
func NewServer(cfg Config, d Deps) *Server {
	if d.Clock == nil {
		d.Clock = RealClock()
	}
	if d.Rand == nil {
		d.Rand = rand.Int64N
	}
	return &Server{cfg: cfg, d: d, rooms: map[string]*room{}, idle: make(chan struct{}, 1)}
}

// Handler serves GET /events/{event_id}/queue/stream and GET /health.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events/{event_id}/queue/stream", s.serveStream)
	mux.HandleFunc("GET /health", s.serveHealth)
	return mux
}

// Connections is the number of registered connections of an event.
func (s *Server) Connections(eventID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.rooms[eventID]; r != nil {
		return len(r.conns)
	}
	return 0
}

func (s *Server) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.rooms {
		n += len(r.conns)
	}
	return n
}

// ---------------------------------------------------------------- connect

func httpErr(status int, errorCode, message string) *apperror.AppError {
	return apperror.New(status, fmt.Sprintf("%d00", status), message).WithCode(errorCode)
}

func unavailable() *apperror.AppError {
	return httpErr(http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Layanan sedang tidak tersedia, coba lagi").WithRetryAfter(5 * time.Second)
}

func (s *Server) serveStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	evID := r.PathValue("event_id")
	if s.isDraining() {
		apinizer.WriteError(w, unavailable())
		return
	}
	ev, err := rediskeys.NewEvent(evID)
	if err != nil {
		apinizer.WriteError(w, httpErr(http.StatusNotFound, "NOT_FOUND", "Event tidak ditemukan"))
		return
	}

	// 1 rate limit per IP; a limiter outage fails open (the stream is cheap)
	res, err := s.d.Limiter.Allow(ctx, rediskeys.SSERateLimit(clientIP(r)), s.cfg.IPLimit)
	switch {
	case err != nil:
		s.d.Metrics.Incr(MetricRateLimitOpen)
		slog.WarnContext(ctx, "stream: rate limiter unavailable, failing open", slog.Any("err", err))
	case !res.Allowed:
		apinizer.WriteError(w, httpErr(http.StatusTooManyRequests, "TOO_MANY_REQUESTS", "Terlalu banyak koneksi, coba lagi").WithRetryAfter(res.RetryAfter))
		return
	}

	// 2 sale state
	state, found, err := s.d.Store.State(ctx, ev)
	if err != nil {
		slog.ErrorContext(ctx, "stream: state read failed", slog.String("event_id", evID), slog.Any("err", err))
		apinizer.WriteError(w, unavailable())
		return
	}
	if !found {
		apinizer.WriteError(w, httpErr(http.StatusNotFound, "NOT_FOUND", "Event tidak ditemukan"))
		return
	}
	switch state {
	case StateOpen, StateFullBooked:
	case StateScheduled:
		apinizer.WriteError(w, httpErr(http.StatusConflict, "SALE_NOT_OPEN", "Penjualan belum dibuka"))
		return
	case StateClosed:
		apinizer.WriteError(w, httpErr(http.StatusGone, "SALE_CLOSED", closedMessage))
		return
	default:
		apinizer.WriteError(w, httpErr(http.StatusServiceUnavailable, "QUEUE_NOT_READY", "Antrian belum siap"))
		return
	}

	// 3 my token: header (mobile) over cookie (web); never the query string
	tok := queueToken(r)
	if tok == "" {
		apinizer.WriteError(w, httpErr(http.StatusUnauthorized, "UNAUTHORIZED", "Token antrian tidak ditemukan"))
		return
	}
	tokenEvent, found, err := s.d.Store.TokenEvent(ctx, tok)
	if err != nil {
		slog.ErrorContext(ctx, "stream: token read failed", slog.String("event_id", evID), slog.Any("err", err))
		apinizer.WriteError(w, unavailable())
		return
	}
	if !found || tokenEvent != evID {
		apinizer.WriteError(w, httpErr(http.StatusUnauthorized, "UNAUTHORIZED", "Token antrian tidak dikenal"))
		return
	}

	// 4 open, and tell the customer where the queue is right now (FR-10)
	nowServing, rate, err := s.d.Store.Numbers(ctx, ev)
	if err != nil {
		slog.ErrorContext(ctx, "stream: numbers read failed", slog.String("event_id", evID), slog.Any("err", err))
		apinizer.WriteError(w, unavailable())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apinizer.WriteError(w, unavailable())
		return
	}

	// register before the headers: a server that started draining in the meantime still answers 503
	c := &conn{ch: make(chan []byte, s.cfg.ConnBuffer), done: make(chan struct{})}
	if !s.register(c, evID, state, nowServing, rate) {
		apinizer.WriteError(w, unavailable())
		return
	}
	defer s.unregister(c, evID)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ka := s.d.Clock.NewTicker(s.cfg.Keepalive)
	defer ka.Stop()
	for {
		select {
		case b := <-c.ch:
			if b == nil {
				return
			}
			if _, err := w.Write(b); err != nil {
				return
			}
			flusher.Flush()
		case <-ka.C():
			if _, err := w.Write(keepaliveFrame); err != nil {
				return
			}
			flusher.Flush()
		case <-c.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

func queueToken(r *http.Request) string {
	if v := r.Header.Get("X-Queue-Token"); v != "" {
		return v
	}
	if c, err := r.Cookie("queue_token"); err == nil {
		return c.Value
	}
	return ""
}

// clientIP is the right-most X-Forwarded-For entry (the ALB's), else the peer.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) isDraining() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.draining
}

// register queues the snapshot first, then adds the connection, so no broadcast
// can reach a customer before their first numbers.
func (s *Server) register(c *conn, evID, state string, nowServing, rate int64) bool {
	now := s.d.Clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return false
	}
	r := s.rooms[evID]
	if r == nil || len(r.conns) == 0 {
		// the first customer starts the clock: nothing is stale about an event nobody watched
		r = &room{id: evID, conns: map[*conn]struct{}{}, state: state, lastFresh: now}
		s.rooms[evID] = r
	}
	// The snapshot was read before the lock: a broadcast or status may have reached this room since.
	// Never show a number or a state older than the room's.
	if nowServing > r.lastNS {
		r.lastNS = nowServing
	}
	nowServing = r.lastNS
	state = r.state
	c.send(updateFrame(nowServing, state, rate, now))
	if r.stale {
		c.send(staleFrame(true, r.reason))
	}
	r.conns[c] = struct{}{}
	s.d.Metrics.Gauge(MetricConnections, evID, float64(len(r.conns)))
	return true
}

func (s *Server) unregister(c *conn, evID string) {
	s.mu.Lock()
	if r := s.rooms[evID]; r != nil {
		if _, ok := r.conns[c]; ok {
			delete(r.conns, c)
			s.d.Metrics.Gauge(MetricConnections, evID, float64(len(r.conns)))
		}
		if len(r.conns) == 0 {
			delete(s.rooms, evID) // also when the fan-out already dropped this connection
		}
	}
	s.mu.Unlock()
	select {
	case s.idle <- struct{}{}:
	default:
	}
}

func (s *Server) serveHealth(w http.ResponseWriter, _ *http.Request) {
	status, code := "ok", http.StatusOK
	switch {
	case s.isDraining():
		status, code = "draining", http.StatusServiceUnavailable
	case !s.connected.Load():
		status = "degraded" // alive, serving what it has, but not hearing admission-ctl
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"status":%q}`, status)
}

// ---------------------------------------------------------------- fan-out (callers hold s.mu)

// broadcast writes the same bytes to every connection; a full buffer drops it.
func (s *Server) broadcast(r *room, b []byte) {
	dropped := false
	for c := range r.conns {
		if !c.send(b) {
			delete(r.conns, c)
			c.kill()
			s.d.Metrics.Incr(MetricBackpressure)
			dropped = true
		}
	}
	if dropped {
		s.d.Metrics.Gauge(MetricConnections, r.id, float64(len(r.conns)))
		if len(r.conns) == 0 {
			delete(s.rooms, r.id)
		}
	}
}

// closeRoom ends the sale for everyone: status CLOSED, sold-out, then the responses end.
func (s *Server) closeRoom(r *room, now time.Time) {
	b := closedFrames(now)
	for c := range r.conns {
		if !c.send(b) || !c.send(nil) { // a full buffer cannot take the ending: cut it off instead
			c.kill()
		}
	}
	r.conns = map[*conn]struct{}{}
	r.state = StateClosed
	delete(s.rooms, r.id)
	s.d.Metrics.Gauge(MetricConnections, r.id, 0)
}

// ---------------------------------------------------------------- messages from admission-ctl

func (s *Server) handle(m Message) {
	switch m.Channel {
	case rediskeys.ChannelBroadcast:
		s.handleBroadcast(m.Payload)
	case rediskeys.ChannelStatus:
		s.handleStatus(m.Payload)
	}
}

func (s *Server) bad(channel string, err error) {
	s.d.Metrics.Incr(MetricBadMessage)
	slog.Warn("stream: dropped a malformed message", slog.String("channel", channel), slog.Any("err", err))
}

func (s *Server) handleBroadcast(payload []byte) {
	var m broadcastMsg
	switch err := json.Unmarshal(payload, &m); {
	case err != nil:
		s.bad(rediskeys.ChannelBroadcast, err)
		return
	case m.EventID == "" || m.NowServing == nil || m.RatePerMin == nil || !validState(m.State):
		s.bad(rediskeys.ChannelBroadcast, errors.New("missing or invalid field"))
		return
	}
	now := s.d.Clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[m.EventID]
	if r == nil {
		return // nobody on this server watches it
	}
	r.lastFresh = now
	if r.stale {
		r.stale, r.reason = false, ""
		s.broadcast(r, staleFrame(false, ""))
	}
	if m.State == StateClosed {
		s.closeRoom(r, now)
		return
	}
	if *m.NowServing < r.lastNS {
		return // a late duplicate: the queue number never goes backwards on a screen
	}
	r.lastNS = *m.NowServing // r.state is changed only by sse-status, which announces it
	s.d.Metrics.Incr(MetricMessageBuilt)
	s.broadcast(r, updateFrame(*m.NowServing, m.State, *m.RatePerMin, now))
}

func (s *Server) handleStatus(payload []byte) {
	var m statusPublished
	switch err := json.Unmarshal(payload, &m); {
	case err != nil:
		s.bad(rediskeys.ChannelStatus, err)
		return
	case m.EventID == "" || !validState(m.State):
		s.bad(rediskeys.ChannelStatus, errors.New("missing or invalid field"))
		return
	}
	now := s.d.Clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.rooms[m.EventID]; r != nil {
		s.applyState(r, m.State, m.FullBookedAt, now)
	}
}

// applyState tells the room about a state change, once.
func (s *Server) applyState(r *room, state string, fullBookedAt int64, now time.Time) {
	if state == r.state {
		return // clients already know it (admission-ctl republishes after a restart)
	}
	if state == StateClosed {
		s.closeRoom(r, now)
		return
	}
	r.state = state
	s.broadcast(r, statusFrame(state, fullBookedAt, now))
}

// ---------------------------------------------------------------- run: subscriber and freshness

// Run subscribes and watches freshness until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	go s.watchFreshness(ctx)
	attempt := 0
	for ctx.Err() == nil {
		sub, err := s.d.Source.Subscribe(ctx)
		if err != nil {
			s.lost()
		} else {
			if s.hadDisconnect.Load() {
				s.resync(ctx)
			}
			s.connected.Store(true)
			s.hadDisconnect.Store(false)
			began := s.d.Clock.Now()
			s.consume(ctx, sub)
			if s.d.Clock.Now().Sub(began) >= s.cfg.BackoffMax { // a flapping subscription keeps backing off
				attempt = 0
			}
			_ = sub.Close()
			if ctx.Err() != nil {
				break
			}
			s.lost()
		}
		if !s.backoff(ctx, attempt) {
			break
		}
		attempt++
	}
	return nil
}

func (s *Server) consume(ctx context.Context, sub Subscription) {
	for {
		select {
		case m, ok := <-sub.Messages():
			if !ok {
				return
			}
			s.handle(m)
		case <-ctx.Done():
			return
		}
	}
}

// lost marks the numbers stale for everyone: Redis (or the link to it) is gone.
func (s *Server) lost() {
	s.connected.Store(false)
	s.hadDisconnect.Store(true)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rooms {
		if !r.stale {
			r.stale, r.reason = true, staleRedisDown
			s.broadcast(r, staleFrame(true, staleRedisDown))
		}
	}
}

// resync re-reads the state of every watched event: pub/sub drops messages while
// disconnected, and a missed FULL_BOOKED / CLOSED must still reach the customer.
func (s *Server) resync(ctx context.Context) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.rooms))
	for id := range s.rooms {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		ev, err := rediskeys.NewEvent(id)
		if err != nil {
			continue
		}
		state, found, err := s.d.Store.State(ctx, ev)
		if err != nil || !found || !validState(state) {
			continue
		}
		now := s.d.Clock.Now()
		s.mu.Lock()
		if r := s.rooms[id]; r != nil {
			s.applyState(r, state, 0, now)
		}
		s.mu.Unlock()
	}
}

// backoff waits before the next subscribe: grows from 100 ms, capped, with jitter.
func (s *Server) backoff(ctx context.Context, attempt int) bool {
	base := s.cfg.BackoffMax
	if attempt < 10 {
		if b := 100 * time.Millisecond << attempt; b < base {
			base = b
		}
	}
	d := base/2 + time.Duration(s.d.Rand(int64(base/2)+1))
	s.d.Metrics.Gauge(MetricResubscribeSec, "", d.Seconds())
	t := s.d.Clock.NewTicker(d)
	defer t.Stop()
	select {
	case <-t.C():
		return true
	case <-ctx.Done():
		return false
	}
}

// watchFreshness says "stale" once when admission-ctl goes quiet, and reports its age.
func (s *Server) watchFreshness(ctx context.Context) {
	t := s.d.Clock.NewTicker(s.cfg.StaleCheck)
	defer t.Stop()
	for {
		select {
		case <-t.C():
			s.checkFreshness(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) checkFreshness(ctx context.Context) {
	now := s.d.Clock.Now()
	var quiet []string
	s.mu.Lock()
	for id, r := range s.rooms {
		age := now.Sub(r.lastFresh)
		s.d.Metrics.Gauge(MetricHeartbeatAge, id, age.Seconds())
		if !r.stale && age > s.cfg.StaleAfter {
			quiet = append(quiet, id)
		}
	}
	s.mu.Unlock()
	for _, id := range quiet {
		// only a sale that can still move can be "stale": not before it opens, not after it closes
		ev, err := rediskeys.NewEvent(id)
		if err != nil {
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		state, found, err := s.d.Store.State(readCtx, ev)
		cancel()
		if err != nil || !found || (state != StateOpen && state != StateFullBooked) {
			continue // Redis trouble is reported as redis_disconnected, not as a silent controller
		}
		s.mu.Lock()
		if r := s.rooms[id]; r != nil && !r.stale && now.Sub(r.lastFresh) > s.cfg.StaleAfter {
			r.stale, r.reason = true, staleNoUpdates
			s.broadcast(r, staleFrame(true, staleNoUpdates))
		}
		s.mu.Unlock()
	}
}

// ---------------------------------------------------------------- drain (SIGTERM)

// Drain tells every customer to reconnect (a jittered retry, so 300k do not
// return at once), refuses new ones, and waits for the rest to leave. At the
// deadline it closes whoever is still there.
func (s *Server) Drain(ctx context.Context) error {
	s.mu.Lock()
	s.draining = true
	spread := int64(s.cfg.RetryMax-s.cfg.RetryMin) / int64(time.Millisecond)
	for _, r := range s.rooms {
		for c := range r.conns {
			retry := int64(s.cfg.RetryMin/time.Millisecond) + s.d.Rand(spread+1)
			c.send(reconnectFrame(retry))
		}
	}
	s.mu.Unlock()

	deadline := s.d.Clock.NewTicker(s.cfg.DrainDeadline)
	defer deadline.Stop()
	for s.total() > 0 {
		select {
		case <-s.idle:
		case <-deadline.C():
			s.mu.Lock()
			for _, r := range s.rooms {
				for c := range r.conns {
					c.kill()
				}
			}
			s.mu.Unlock()
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
