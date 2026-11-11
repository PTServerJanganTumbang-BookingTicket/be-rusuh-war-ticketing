package template

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

func renderer() *Renderer {
	return NewRenderer(Links{AppBase: "https://rusuh.id", MagicTokenTTLMinutes: 15})
}

func envelopeOf(t *testing.T, eventType events.Type, payload any) events.Envelope {
	t.Helper()
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	return events.Envelope{OutboxID: "evt-1", Type: eventType, Payload: encoded}
}

func TestRender_EveryKnownTypeHasBothLocales(t *testing.T) {
	// A type that reaches the queue with no template is parked, not retried,
	// so a missing entry is a silently undelivered email.
	for _, eventType := range []events.Type{
		events.TypeAuthMagicLink, events.TypeAccountVerify,
		events.TypeTicketIssue, events.TypeBookingExpired,
		events.TypePaymentFailedNotice,
	} {
		byLocale, ok := catalog[eventType]
		require.True(t, ok, "no catalog entry for %s", eventType)
		for _, locale := range []events.Locale{events.LocaleID, events.LocaleEN} {
			def, ok := byLocale[locale]
			require.True(t, ok, "%s has no %s template", eventType, locale)
			assert.NotEmpty(t, def.subject)
			assert.NotEmpty(t, def.html)
			assert.NotEmpty(t, def.text)
		}
	}
}

func TestRender_MagicLink(t *testing.T) {
	rec, out, err := renderer().Render(envelopeOf(t, events.TypeAuthMagicLink,
		events.AuthMagicLinkPayload{Email: "user@example.com", Token: "tok-123"}))
	require.NoError(t, err)

	assert.Equal(t, "user@example.com", rec.Email)
	assert.Equal(t, events.LocaleID, rec.Locale, "Indonesian is the default")
	assert.Contains(t, out.HTMLBody, "https://rusuh.id/auth/login?token=tok-123")
	assert.Contains(t, out.TextBody, "tok-123")
	assert.Contains(t, out.HTMLBody, "15")
}

func TestRender_LinksPointAtTheAppNotTheAPI(t *testing.T) {
	// The verify endpoint reads its token from a cookie (contract §1.3), so a
	// link straight to the API would 400.
	_, out, err := renderer().Render(envelopeOf(t, events.TypeAccountVerify,
		events.AccountVerifyPayload{Email: "a@example.com", Token: "tok"}))
	require.NoError(t, err)
	assert.Contains(t, out.HTMLBody, "https://rusuh.id/auth/verify-email?token=tok")
}

func TestRender_EnglishLocale(t *testing.T) {
	rec, out, err := renderer().Render(envelopeOf(t, events.TypeAuthMagicLink,
		events.AuthMagicLinkPayload{Email: "a@example.com", Token: "t", Locale: events.LocaleEN}))
	require.NoError(t, err)
	assert.Equal(t, events.LocaleEN, rec.Locale)
	assert.Contains(t, out.Subject, "sign-in")
}

func TestRender_UnsupportedLocaleFallsBackToIndonesian(t *testing.T) {
	rec, out, err := renderer().Render(envelopeOf(t, events.TypeAuthMagicLink,
		events.AuthMagicLinkPayload{Email: "a@example.com", Token: "t", Locale: events.Locale("fr")}))
	require.NoError(t, err)
	assert.Equal(t, events.LocaleID, rec.Locale)
	assert.Contains(t, out.Subject, "masuk")
}

func TestRender_HTMLEscapesUserControlledValues(t *testing.T) {
	// A ticket's category and a saved NIK's label originate in user input. In
	// an HTML email body they would otherwise be an injection vector in
	// someone else's inbox.
	_, out, err := renderer().Render(envelopeOf(t, events.TypeTicketIssue,
		events.TicketIssuePayload{
			TicketID: "TKT-1", ContactEmail: "h@example.com",
			EventName: `Konser <script>alert(1)</script>`,
			Category:  `VIP" onload="x`,
		}))
	require.NoError(t, err)
	assert.NotContains(t, out.HTMLBody, "<script>")
	assert.Contains(t, out.HTMLBody, "&lt;script&gt;")
}

// ADR-006 §8.15: every e-ticket of a booking goes to contact_email. The seat
// never carried an address, so there is no holder inbox to address - one link
// per seat still goes out, and the buyer forwards each on.
func TestRender_TicketIssueAddressesTheContactEmail(t *testing.T) {
	rec, out, err := renderer().Render(envelopeOf(t, events.TypeTicketIssue,
		events.TicketIssuePayload{
			TicketID: "TKT-001", ContactEmail: "buyer@example.com",
			EventName: "Konser A", EventDate: "2026-12-25", EventVenue: "GBK",
			Category: "VIP", NIKMasked: "3171**********22",
		}))
	require.NoError(t, err)
	assert.Equal(t, "buyer@example.com", rec.Email, "FR-26 via §8.15: one link per seat, to the buyer")
	assert.Contains(t, out.HTMLBody, "https://rusuh.id/tickets/TKT-001/eticket")
	assert.Contains(t, out.HTMLBody, "3171**********22")
}

// Rows written before §8.15 are still in the outbox when this deploys, and
// they carry holder_email and no contact_email. Dropping them would lose a
// paid customer's e-ticket, so the old field is read - and only read.
func TestRender_TicketIssue_FallsBackToTheLegacyHolderEmail(t *testing.T) {
	rec, _, err := renderer().Render(envelopeOf(t, events.TypeTicketIssue,
		events.TicketIssuePayload{
			TicketID: "TKT-001", LegacyHolderEmail: "in-flight@example.com",
			EventName: "Konser A", EventDate: "2026-12-25", EventVenue: "GBK",
			Category: "VIP", NIKMasked: "3171**********22",
		}))
	require.NoError(t, err)
	assert.Equal(t, "in-flight@example.com", rec.Email)
}

// With both present the new field wins: a row re-enqueued by a replay must not
// be routed by a field the producer no longer writes.
func TestRender_TicketIssue_ContactEmailBeatsTheLegacyField(t *testing.T) {
	rec, _, err := renderer().Render(envelopeOf(t, events.TypeTicketIssue,
		events.TicketIssuePayload{
			TicketID: "TKT-001", ContactEmail: "buyer@example.com",
			LegacyHolderEmail: "stale@example.com",
			EventName:         "Konser A", EventDate: "2026-12-25", EventVenue: "GBK",
			Category: "VIP", NIKMasked: "3171**********22",
		}))
	require.NoError(t, err)
	assert.Equal(t, "buyer@example.com", rec.Email)
}

func TestRender_PaymentFailedNotice_DisputedStatesFundsReceived(t *testing.T) {
	// FR-28b: condition (c) must say the money was received and a refund is
	// being processed. Telling this customer "payment did not complete" would
	// be both false and alarming.
	_, out, err := renderer().Render(envelopeOf(t, events.TypePaymentFailedNotice,
		events.PaymentFailedNoticePayload{
			BookingID: "RB-00912", OrderID: "RB-00912:2", ContactEmail: "b@example.com",
			EventName: "Konser A", GrossAmount: 1500000, Reason: events.FailureDisputed,
		}))
	require.NoError(t, err)
	assert.Contains(t, out.TextBody, "pengembalian dana")
	assert.Contains(t, out.TextBody, "Rp1.500.000")
	assert.NotContains(t, out.TextBody, "Tidak ada dana yang terpotong")
}

func TestRender_PaymentFailedNotice_TTLElapsedSaysNoCharge(t *testing.T) {
	_, out, err := renderer().Render(envelopeOf(t, events.TypePaymentFailedNotice,
		events.PaymentFailedNoticePayload{
			BookingID: "RB-1", ContactEmail: "b@example.com",
			EventName: "Konser A", Reason: events.FailureTTLElapsed,
		}))
	require.NoError(t, err)
	assert.Contains(t, out.TextBody, "Tidak ada dana yang terpotong")
	assert.NotContains(t, out.TextBody, "pengembalian dana")
}

func TestRender_PaymentFailedNotice_GatewayDeclined(t *testing.T) {
	_, out, err := renderer().Render(envelopeOf(t, events.TypePaymentFailedNotice,
		events.PaymentFailedNoticePayload{
			BookingID: "RB-1", ContactEmail: "b@example.com",
			EventName: "Konser A", Reason: events.FailureGatewayDeclined,
		}))
	require.NoError(t, err)
	assert.Contains(t, out.TextBody, "ditolak")
	assert.Contains(t, out.TextBody, "Tidak ada dana yang terpotong")
}

func TestRender_UnknownTypeIsErrNoTemplate(t *testing.T) {
	_, _, err := renderer().Render(events.Envelope{
		OutboxID: "evt-1", Type: events.Type("payment.refund"), Payload: []byte(`{}`),
	})
	var noTemplate ErrNoTemplate
	require.ErrorAs(t, err, &noTemplate)
	assert.Equal(t, events.Type("payment.refund"), noTemplate.Type)
}

func TestRender_MalformedPayload(t *testing.T) {
	_, _, err := renderer().Render(events.Envelope{
		OutboxID: "evt-1", Type: events.TypeAuthMagicLink, Payload: []byte(`{not json`),
	})
	assert.Error(t, err)
}

func TestNewRenderer_DefaultsTheTTL(t *testing.T) {
	r := NewRenderer(Links{AppBase: "https://rusuh.id"})
	assert.Equal(t, 15, r.links.MagicTokenTTLMinutes)
}

func TestLinkTrimsTrailingSlash(t *testing.T) {
	r := NewRenderer(Links{AppBase: "https://rusuh.id/", MagicTokenTTLMinutes: 15})
	assert.Equal(t, "https://rusuh.id/auth/login?token=t", r.link("/auth/login", "t"))
}

func TestFormatIDR(t *testing.T) {
	cases := map[int64]string{
		0:       "",
		-1:      "",
		500:     "Rp500",
		1500:    "Rp1.500",
		15000:   "Rp15.000",
		150000:  "Rp150.000",
		1500000: "Rp1.500.000",
		1000:    "Rp1.000",
	}
	for amount, want := range cases {
		assert.Equal(t, want, formatIDR(amount), "amount %d", amount)
	}
}

func TestRenderedBodiesAreNotEmpty(t *testing.T) {
	_, out, err := renderer().Render(envelopeOf(t, events.TypeBookingExpired,
		events.BookingExpiredPayload{
			BookingID: "RB-1", ContactEmail: "b@example.com", EventName: "Konser A",
		}))
	require.NoError(t, err)
	assert.NotEmpty(t, strings.TrimSpace(out.Subject))
	assert.NotEmpty(t, strings.TrimSpace(out.HTMLBody))
	assert.NotEmpty(t, strings.TrimSpace(out.TextBody))
	assert.Contains(t, out.HTMLBody, "<!doctype html>")
}
