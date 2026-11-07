package stream

import (
	"encoding/json"
	"fmt"
	"time"
)

// Sale states (api-contracts §3.2). FULL_BOOKED is reversible; CLOSED is final.
const (
	StateScheduled  = "SCHEDULED"
	StateOpen       = "OPEN"
	StateFullBooked = "FULL_BOOKED"
	StateClosed     = "CLOSED"
)

// closedMessage is what a customer reads when the sale is over.
const closedMessage = "Tiket sudah habis"

func validState(s string) bool {
	return s == StateScheduled || s == StateOpen || s == StateFullBooked || s == StateClosed
}

// iso is ISO-8601 UTC with milliseconds (BR-10).
func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

type update struct {
	NowServing int64  `json:"nowServing"`
	State      string `json:"state"`
	RatePerMin int64  `json:"ratePerMin"`
	ServerTime string `json:"serverTime"`
}

type statusMsg struct {
	State        string  `json:"state"`
	FullBookedAt *string `json:"fullBookedAt"`
	ServerTime   string  `json:"serverTime"`
}

type staleMsg struct {
	Stale  bool   `json:"stale"`
	Reason string `json:"reason,omitempty"`
}

func must(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil { // plain structs of strings and numbers cannot fail to marshal
		panic(err)
	}
	return b
}

// updateFrame is the default event. The same bytes go to every customer.
func updateFrame(nowServing int64, state string, ratePerMin int64, now time.Time) []byte {
	return []byte(fmt.Sprintf("id: %d\ndata: %s\n\n", nowServing,
		must(update{NowServing: nowServing, State: state, RatePerMin: ratePerMin, ServerTime: iso(now)})))
}

// statusFrame says the sale state changed. fullBookedAt is epoch seconds, 0 for none.
func statusFrame(state string, fullBookedAt int64, now time.Time) []byte {
	var at *string
	if state == StateFullBooked && fullBookedAt > 0 {
		s := iso(time.Unix(fullBookedAt, 0))
		at = &s
	}
	return []byte(fmt.Sprintf("event: status\ndata: %s\n\n", must(statusMsg{State: state, FullBookedAt: at, ServerTime: iso(now)})))
}

func soldOutFrame() []byte {
	return []byte(fmt.Sprintf("event: sold-out\ndata: %s\n\n", must(map[string]string{"message": closedMessage})))
}

// closedFrames is status CLOSED followed by sold-out; the response ends after it.
func closedFrames(now time.Time) []byte {
	return append(statusFrame(StateClosed, 0, now), soldOutFrame()...)
}

func staleFrame(stale bool, reason string) []byte {
	return []byte(fmt.Sprintf("event: stale\ndata: %s\n\n", must(staleMsg{Stale: stale, Reason: reason})))
}

func reconnectFrame(retryMS int64) []byte {
	return []byte(fmt.Sprintf("event: reconnect\nretry: %d\ndata: {}\n\n", retryMS))
}

var keepaliveFrame = []byte(": keepalive\n\n")

// broadcastMsg is what admission-ctl publishes on sse-broadcast.
type broadcastMsg struct {
	EventID    string `json:"eventId"`
	NowServing *int64 `json:"nowServing"`
	State      string `json:"state"`
	RatePerMin *int64 `json:"ratePerMin"`
	ServerTime int64  `json:"serverTime"`
}

// statusPublished is what admission-ctl publishes on sse-status.
type statusPublished struct {
	EventID      string `json:"eventId"`
	State        string `json:"state"`
	FullBookedAt int64  `json:"fullBookedAt"`
	ServerTime   int64  `json:"serverTime"`
}
