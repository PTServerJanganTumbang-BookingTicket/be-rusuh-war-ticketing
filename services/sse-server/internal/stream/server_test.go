package stream

import (
	"strings"
	"testing"
	"time"
)

type nopMetrics struct{}

func (nopMetrics) Incr(string)                   {}
func (nopMetrics) Gauge(string, string, float64) {}

func newTestServer(buffer int) *Server {
	cfg := DefaultConfig()
	cfg.ConnBuffer = buffer
	return NewServer(cfg, Deps{Metrics: nopMetrics{}, Clock: RealClock()})
}

func newConn(buffer int) *conn {
	return &conn{ch: make(chan []byte, buffer), done: make(chan struct{})}
}

func drain(c *conn) string {
	var b strings.Builder
	for {
		select {
		case f := <-c.ch:
			b.Write(f)
		default:
			return b.String()
		}
	}
}

const broadcastE1 = `{"eventId":"E1","nowServing":%d,"state":"%s","ratePerMin":318,"serverTime":1}`

func broadcast(n int, state string) []byte {
	return []byte(strings.NewReplacer("%d", itoa(n), "%s", state).Replace(broadcastE1))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

// Review: a customer whose buffer is full when the sale closes must still be cut off,
// otherwise its response never ends.
func TestCloseRoom_cutsOffAConnectionThatCannotTakeTheEnding(t *testing.T) {
	s := newTestServer(2)
	slow := newConn(2)
	s.register(slow, "E1", StateOpen, 100, 318) // the snapshot fills one slot
	slow.ch <- []byte("x")                      // and the buffer is now full
	s.mu.Lock()
	s.closeRoom(s.rooms["E1"], time.Now())
	s.mu.Unlock()
	select {
	case <-slow.done:
	default:
		t.Fatal("a connection that could not take the ending was left open")
	}
}

// Review: a broadcast must not swallow the later sse-status for the same change.
func TestStatus_isStillAnnouncedAfterABroadcastCarriedTheSameState(t *testing.T) {
	s := newTestServer(8)
	c := newConn(8)
	s.register(c, "E1", StateOpen, 100, 318)
	drain(c)
	s.handleBroadcast(broadcast(101, StateFullBooked))
	s.handleStatus([]byte(`{"eventId":"E1","state":"FULL_BOOKED","fullBookedAt":1792847380,"serverTime":1}`))
	if got := drain(c); !strings.Contains(got, "event: status") || !strings.Contains(got, `"state":"FULL_BOOKED"`) {
		t.Fatalf("no status event after the broadcast; got %q", got)
	}
}

// Review: when the fan-out drops the last connection, the empty room must go too.
func TestBroadcast_removesTheRoomWhenTheLastConnectionIsDropped(t *testing.T) {
	s := newTestServer(2)
	c := newConn(2)
	s.register(c, "E1", StateOpen, 100, 318) // 1 of 2 slots used
	s.handleBroadcast(broadcast(101, StateOpen))
	s.handleBroadcast(broadcast(102, StateOpen)) // the buffer is full: dropped
	s.unregister(c, "E1")
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rooms["E1"]; ok {
		t.Fatal("an empty room was kept (and would be checked forever)")
	}
}

// Review: the snapshot is read before the lock; it must never show an older number
// or state than the room already has.
func TestRegister_neverShowsAnOlderSnapshotThanTheRoom(t *testing.T) {
	s := newTestServer(8)
	first := newConn(8)
	s.register(first, "E1", StateOpen, 100, 318)
	s.handleBroadcast(broadcast(105, StateOpen))
	s.handleStatus([]byte(`{"eventId":"E1","state":"FULL_BOOKED","fullBookedAt":1792847380,"serverTime":1}`))

	late := newConn(8)
	s.register(late, "E1", StateOpen, 100, 318) // read from Redis before the 105 / FULL_BOOKED arrived
	got := drain(late)
	if !strings.Contains(got, `"nowServing":105`) || !strings.Contains(got, `"state":"FULL_BOOKED"`) {
		t.Fatalf("snapshot went backwards: %q", got)
	}
}

// Review: a server that started draining after the checks must refuse, not open an empty stream.
func TestRegister_refusesWhileDraining(t *testing.T) {
	s := newTestServer(8)
	s.mu.Lock()
	s.draining = true
	s.mu.Unlock()
	if s.register(newConn(8), "E1", StateOpen, 1, 1) {
		t.Fatal("registered a connection on a draining server")
	}
}
