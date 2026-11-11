// Package rediskeys builds every Redis key and pub/sub channel name used by the
// queue domain. Never hand-write a key string anywhere else.
//
// Only "{ev}" is a real Redis hash tag: every key of one event shares it, so
// Redis cluster puts them in one slot and a Lua script may touch them together.
// Per-customer keys (token, grant, rate limits) carry no event tag on purpose;
// a script must never mix them with event keys (impl-plan §3.1, ADR-006 §5).
package rediskeys

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ErrInvalidEventID is returned for an event id that would break the {ev} hash tag.
var ErrInvalidEventID = errors.New("invalid event id")

// Pub/sub channels (ADR-006 rule 2: one broadcast, no grant channel).
const (
	ChannelBroadcast = "sse-broadcast"
	ChannelStatus    = "sse-status"
)

// Event is a validated event id. Its keys all share the {id} hash tag.
type Event struct{ id string }

// NewEvent validates id: non-empty, no braces (they would end the hash tag
// early) and no whitespace or control characters.
func NewEvent(id string) (Event, error) {
	if id == "" {
		return Event{}, fmt.Errorf("%w: empty", ErrInvalidEventID)
	}
	if i := strings.IndexFunc(id, func(r rune) bool {
		return r == '{' || r == '}' || unicode.IsSpace(r) || unicode.IsControl(r)
	}); i >= 0 {
		return Event{}, fmt.Errorf("%w: %q has a forbidden character at %d", ErrInvalidEventID, id, i)
	}
	return Event{id: id}, nil
}

// ID returns the raw event id.
func (e Event) ID() string { return e.id }

func (e Event) tag() string { return "{" + e.id + "}" }

// QueueHash is queue:{ev}, the event's one hash (ADR-006 §8.12): state
// (SCHEDULED | OPEN | FULL_BOOKED | CLOSED), issued (HINCRBY hands out
// ticket_no), now_serving, full_booked_at (epoch s),
// rate_per_min, name, sale_starts_at. docs/redis-keys.md lists every field.
func (e Event) QueueHash() string { return "queue:" + e.tag() }

// ByTicket is queue:{ev}:by_ticket, a ZSET of queue_token scored by ticket_no.
func (e Event) ByTicket() string { return "queue:" + e.tag() + ":by_ticket" }

// Stock is stock:{ev}:<category>.
func (e Event) Stock(category string) string { return "stock:" + e.tag() + ":" + category }

// NIKUsed is nik:used:{ev}:<nik_hmac>, "this NIK holds a seat for this event": a fast duplicate check set by
// hold_booking.lua. The database (nik_claims) is the rule. It shares the {ev} tag with the stock keys, so one script
// call can touch both.
func (e Event) NIKUsed(nikHmac string) string { return "nik:used:" + e.tag() + ":" + nikHmac }

// Acct is acct:{ev}:<account_id>, the account's one engagement (BR-07).
func (e Event) Acct(accountID string) string { return "acct:" + e.tag() + ":" + accountID }

// Token is token:<queue_token>, the engagement hash.
func Token(queueToken string) string { return "token:" + queueToken }

// Grant is grant:<grant_token>.
func Grant(grantToken string) string { return "grant:" + grantToken }

// Settled is the tombstone of a paid booking: a plain string holding the booking_id and nothing else (api-contracts §3.3).
func Settled(queueToken string) string { return "settled:" + queueToken }

// JoinRateLimit is rl:join:<ip> (20 joins / 5 min).
func JoinRateLimit(ip string) string { return "rl:join:" + ip }

// JoinAcctLimit is rl:joinacct:<account_id> (3 joins / min).
func JoinAcctLimit(accountID string) string { return "rl:joinacct:" + accountID }

// SSERateLimit is rl:sse:<ip> (5 stream connections / s).
func SSERateLimit(ip string) string { return "rl:sse:" + ip }
