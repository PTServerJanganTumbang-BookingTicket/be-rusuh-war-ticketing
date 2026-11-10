package template

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

// Links are the base URLs interpolated into emails.
type Links struct {
	// VerifyBase receives the magic token as a query parameter for display,
	// but the endpoint reads the token from a cookie, so the link lands on a
	// frontend route that sets the cookie and then calls the API.
	AppBase string
	// MagicTokenTTLMinutes is quoted in the body so the customer knows how
	// long they have.
	MagicTokenTTLMinutes int
}

// Recipient is who an email goes to, resolved from the payload.
type Recipient struct {
	Email  string
	Locale events.Locale
}

// Renderer turns an envelope into a rendered email.
type Renderer struct {
	links Links
}

// NewRenderer returns a Renderer.
func NewRenderer(links Links) *Renderer {
	if links.MagicTokenTTLMinutes <= 0 {
		links.MagicTokenTTLMinutes = 15
	}
	return &Renderer{links: links}
}

// Render decodes the payload and produces the email for its type.
//
// Returns ErrNoTemplate for a type with no email defined, which is a producer
// deployed ahead of this consumer rather than a transient failure.
//
// Returns the recipient separately rather than inside Rendered because the
// caller needs it even when rendering fails, to log which address was affected.
func (r *Renderer) Render(envelope events.Envelope) (Recipient, Rendered, error) {
	switch envelope.Type {
	case events.TypeAuthMagicLink:
		var p events.AuthMagicLinkPayload
		if err := json.Unmarshal(envelope.Payload, &p); err != nil {
			return Recipient{}, Rendered{}, fmt.Errorf("decode %s: %w", envelope.Type, err)
		}
		rec := Recipient{Email: p.Email, Locale: p.Locale.Normalize()}
		out, err := r.renderFor(envelope.Type, rec.Locale, map[string]any{
			"Link":       r.link("/auth/login", p.Token),
			"TTLMinutes": r.links.MagicTokenTTLMinutes,
		})
		return rec, out, err

	case events.TypeAccountVerify:
		var p events.AccountVerifyPayload
		if err := json.Unmarshal(envelope.Payload, &p); err != nil {
			return Recipient{}, Rendered{}, fmt.Errorf("decode %s: %w", envelope.Type, err)
		}
		rec := Recipient{Email: p.Email, Locale: p.Locale.Normalize()}
		out, err := r.renderFor(envelope.Type, rec.Locale, map[string]any{
			"Link":       r.link("/auth/verify-email", p.Token),
			"TTLMinutes": r.links.MagicTokenTTLMinutes,
		})
		return rec, out, err

	case events.TypeTicketIssue:
		var p events.TicketIssuePayload
		if err := json.Unmarshal(envelope.Payload, &p); err != nil {
			return Recipient{}, Rendered{}, fmt.Errorf("decode %s: %w", envelope.Type, err)
		}
		// ADR-006 §8.15 (FR-26 as amended): every e-ticket of a booking goes to
		// the BUYER's address, who forwards the links. LegacyHolderEmail is the
		// fallback for rows queued before that change and still on the queue -
		// delivering one to the old address beats not delivering it.
		to := p.ContactEmail
		if to == "" {
			to = p.LegacyHolderEmail
		}
		rec := Recipient{Email: to, Locale: p.Locale.Normalize()}
		out, err := r.renderFor(envelope.Type, rec.Locale, map[string]any{
			"TicketID":    p.TicketID,
			"EventName":   p.EventName,
			"EventDate":   p.EventDate,
			"EventVenue":  p.EventVenue,
			"Category":    p.Category,
			"NIKMasked":   p.NIKMasked,
			"ETicketLink": r.links.AppBase + "/tickets/" + p.TicketID + "/eticket",
		})
		return rec, out, err

	case events.TypeBookingExpired:
		var p events.BookingExpiredPayload
		if err := json.Unmarshal(envelope.Payload, &p); err != nil {
			return Recipient{}, Rendered{}, fmt.Errorf("decode %s: %w", envelope.Type, err)
		}
		rec := Recipient{Email: p.ContactEmail, Locale: p.Locale.Normalize()}
		out, err := r.renderFor(envelope.Type, rec.Locale, map[string]any{
			"BookingID": p.BookingID,
			"EventName": p.EventName,
		})
		return rec, out, err

	case events.TypePaymentFailedNotice:
		var p events.PaymentFailedNoticePayload
		if err := json.Unmarshal(envelope.Payload, &p); err != nil {
			return Recipient{}, Rendered{}, fmt.Errorf("decode %s: %w", envelope.Type, err)
		}
		rec := Recipient{Email: p.ContactEmail, Locale: p.Locale.Normalize()}
		out, err := r.renderFor(envelope.Type, rec.Locale, map[string]any{
			"BookingID": p.BookingID,
			"OrderID":   p.OrderID,
			"EventName": p.EventName,
			// The three FR-28a conditions are passed as flags rather than the
			// raw reason, so the template cannot silently fall through to the
			// wrong branch if a new reason is added.
			"IsDisputed":   p.Reason == events.FailureDisputed,
			"IsTTLElapsed": p.Reason == events.FailureTTLElapsed,
			"AmountText":   formatIDR(p.GrossAmount),
		})
		return rec, out, err

	default:
		return Recipient{}, Rendered{}, ErrNoTemplate{Type: envelope.Type}
	}
}

func (r *Renderer) renderFor(eventType events.Type, locale events.Locale, data any) (Rendered, error) {
	byLocale, ok := catalog[eventType]
	if !ok {
		return Rendered{}, ErrNoTemplate{Type: eventType}
	}
	def, ok := byLocale[locale]
	if !ok {
		// Normalize already guarantees a supported locale, so this only fires
		// if a catalog entry is missing a language.
		def, ok = byLocale[events.LocaleID]
		if !ok {
			return Rendered{}, ErrNoTemplate{Type: eventType}
		}
	}
	return def.render(data)
}

// link builds an app URL carrying a token.
//
// It points at the frontend, not the API: the API reads the magic token from a
// cookie (contract §1.3), so the app route is what sets the cookie and then
// calls the endpoint. A link straight to the API would 400.
func (r *Renderer) link(path, token string) string {
	base := strings.TrimRight(r.links.AppBase, "/")
	return base + path + "?token=" + token
}

// formatIDR renders an amount as Indonesian rupiah with thousands separators,
// or empty when there is no amount to state.
func formatIDR(amount int64) string {
	if amount <= 0 {
		return ""
	}
	digits := strconv.FormatInt(amount, 10)
	var out strings.Builder
	out.WriteString("Rp")
	lead := len(digits) % 3
	if lead == 0 {
		lead = 3
	}
	out.WriteString(digits[:lead])
	for i := lead; i < len(digits); i += 3 {
		out.WriteByte('.')
		out.WriteString(digits[i : i+3])
	}
	return out.String()
}
