package entity

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

func TestRecord_Envelope(t *testing.T) {
	payload, err := json.Marshal(events.AccountVerifyPayload{Email: "a@example.com", Token: "tok"})
	require.NoError(t, err)

	env := Record{
		ID: "evt-1", AggregateID: "USR-001",
		Type: events.TypeAccountVerify, Payload: payload,
		CreatedAt: time.Now().UTC(),
	}.Envelope()

	assert.Equal(t, "evt-1", env.OutboxID, "the consumer dedupes on this")
	assert.Equal(t, "USR-001", env.AggregateID)
	assert.Equal(t, events.TypeAccountVerify, env.Type)
	assert.JSONEq(t, string(payload), string(env.Payload))
}

func TestLeaseTTL_GivesAMarginOverTheWork(t *testing.T) {
	// ADR-005 §9 step 3: 30 seconds against ~60ms of work. The point of the
	// TTL is that a crashed holder's claim lapses — a permanent flag would
	// make every later pass skip the row and lose the event.
	assert.Equal(t, 30*time.Second, LeaseTTL)
	assert.Greater(t, LeaseDoneTTL, LeaseTTL)
}

func TestBatchSize_MatchesTheADR(t *testing.T) {
	assert.Equal(t, 500, BatchSize)
}
