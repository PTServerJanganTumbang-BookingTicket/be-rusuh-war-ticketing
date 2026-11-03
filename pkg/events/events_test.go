package events

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestType_IsKnown(t *testing.T) {
	for _, known := range []Type{
		TypeAccountVerify, TypeAuthMagicLink, TypeTicketIssue,
		TypeBookingExpired, TypePaymentFailedNotice,
	} {
		assert.True(t, known.IsKnown(), "%s", known)
	}
	assert.False(t, Type("").IsKnown())
	assert.False(t, Type("payment.refund").IsKnown(), "not built in the POC")
	assert.False(t, Type("account.deleted").IsKnown())
}

func TestTypeValuesAreStable(t *testing.T) {
	// These strings are on the wire and in the database. Changing one strands
	// every unpublished row that already carries the old spelling.
	assert.Equal(t, "account.verify", string(TypeAccountVerify))
	assert.Equal(t, "auth.magic_link", string(TypeAuthMagicLink))
	assert.Equal(t, "ticket.issue", string(TypeTicketIssue))
	assert.Equal(t, "booking.expired", string(TypeBookingExpired))
	assert.Equal(t, "payment.failed_notice", string(TypePaymentFailedNotice))
}

func TestLocale_Normalize(t *testing.T) {
	assert.Equal(t, LocaleEN, LocaleEN.Normalize())
	assert.Equal(t, LocaleID, LocaleID.Normalize())
	assert.Equal(t, LocaleID, Locale("").Normalize(), "default is Indonesian")
	assert.Equal(t, LocaleID, Locale("fr").Normalize(), "unsupported falls back")
}

func TestEnvelope_RoundTrip(t *testing.T) {
	payload, err := json.Marshal(AccountVerifyPayload{
		AccountID: "USR-001", Email: "a@example.com", Token: "tok", Locale: LocaleID,
	})
	require.NoError(t, err)

	raw, err := json.Marshal(Envelope{
		OutboxID: "01J0", AggregateID: "USR-001", Type: TypeAccountVerify, Payload: payload,
	})
	require.NoError(t, err)

	var back Envelope
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, TypeAccountVerify, back.Type)
	assert.Equal(t, "01J0", back.OutboxID)

	var decoded AccountVerifyPayload
	require.NoError(t, json.Unmarshal(back.Payload, &decoded))
	assert.Equal(t, "a@example.com", decoded.Email)
	assert.Equal(t, "tok", decoded.Token)
}

func TestFailureReasonsCoverFR28a(t *testing.T) {
	// FR-28a names exactly three conditions; FR-28b makes the third an email.
	assert.Equal(t, "gateway_declined", string(FailureGatewayDeclined))
	assert.Equal(t, "ttl_elapsed", string(FailureTTLElapsed))
	assert.Equal(t, "disputed", string(FailureDisputed))
}
