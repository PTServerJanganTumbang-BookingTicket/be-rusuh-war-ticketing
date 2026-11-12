package domain

import "time"

// Serving is what GET /queue/serving reads from queue:{ev}: the same facts as one SSE update frame.
type Serving struct {
	State      SaleState
	NowServing int64
	RatePerMin int64
}

// PollTier is one row of the poll interval table: a client whose position is at most MaxPosition waits PollAfter
// before the next poll. MaxPosition 0 means no limit (null in JSON).
type PollTier struct {
	MaxPosition int64
	PollAfter   time.Duration
}

// DefaultPollAfter is the wait for a client that doesn't know its position (api-contracts §3.4 poll_after_ms).
const DefaultPollAfter = time.Second

// pollTiers is the server-owned table (requirements §5, ADR-006 §8.5). The endpoint takes no query string, so the
// server can't know the caller's position: it sends the whole table and the client picks the first row with
// max_position ≥ its position.
var pollTiers = [...]PollTier{
	{MaxPosition: 10, PollAfter: time.Second},
	{MaxPosition: 100, PollAfter: 2 * time.Second},
	{MaxPosition: 1000, PollAfter: 5 * time.Second},
	{MaxPosition: 0, PollAfter: 10 * time.Second},
}

// PollTiers returns a copy of the table, ordered by MaxPosition with the unlimited row last.
func PollTiers() []PollTier {
	t := pollTiers
	return t[:]
}
