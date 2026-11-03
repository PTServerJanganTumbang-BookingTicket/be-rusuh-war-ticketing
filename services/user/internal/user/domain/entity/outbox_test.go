package entity

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

func TestNewOutboxEvent(t *testing.T) {
	e, err := NewOutboxEvent("evt-1", "USR-001", events.TypeAccountVerify,
		events.AccountVerifyPayload{AccountID: "USR-001", Email: "a@example.com", Token: "tok"})
	require.NoError(t, err)

	assert.Equal(t, "evt-1", e.ID)
	assert.Equal(t, "USR-001", e.AggregateID)
	assert.Equal(t, events.TypeAccountVerify, e.Type)
	assert.Nil(t, e.PublishedAt, "a new row is unpublished")
	assert.False(t, e.CreatedAt.IsZero())

	var back events.AccountVerifyPayload
	require.NoError(t, json.Unmarshal(e.Payload, &back))
	assert.Equal(t, "tok", back.Token)
}

func TestNewOutboxEvent_RejectsUnmarshallablePayload(t *testing.T) {
	// Encoding here rather than in the adapter means this fails before the
	// transaction opens, not halfway through it.
	_, err := NewOutboxEvent("evt-1", "USR-001", events.TypeAccountVerify,
		map[string]any{"bad": math.Inf(1)})
	assert.Error(t, err)
}
