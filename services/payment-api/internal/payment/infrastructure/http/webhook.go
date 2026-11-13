package http

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/awslabs/aws-lambda-go-api-proxy/core"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
)

// Webhook handles POST /payments/webhook (§5.5).
//
// It answers 200 to everything. Not sloppiness — a deliberate contract:
//
//   - Midtrans retries any non-2xx. A 409 for a notification we have already
//     applied would turn one event into a retry storm during the exact minutes
//     the system is busiest.
//   - A 401 for a bad signature tells a prober their guess was wrong, which is
//     a free oracle. An ignored notification looks identical to an accepted
//     one from the outside.
//   - A 500 for our own bug would make Midtrans redeliver something that will
//     fail again. The AWAITING_PAYMENT sweep is the backstop instead: it polls
//     the gateway before expiring and counts AwaitingSweptSettled when it finds
//     a booking the webhook should have confirmed.
//
// Everything interesting is therefore in the log, not the response.
func (c *PaymentController) Webhook(w http.ResponseWriter, r *http.Request) {
	// IP allowlist check — defense in depth alongside signature verification.
	// An empty allowlist disables the check (local dev only; config validation
	// rejects an empty allowlist outside local).
	if len(c.webhookIPAllowlist) > 0 {
		ip := webhookClientIP(r)
		if !c.webhookIPAllowed(ip) {
			// ERROR level, not WARN: this is a security event. The
			// WebhookForbiddenIPFilter metric filter (template-cfn.yaml) extracts
			// WebhookForbiddenIP from this log line, and the alarm in
			// observability/template.yaml fires on any non-zero count.
			slog.ErrorContext(r.Context(), "webhook_forbidden_ip",
				slog.String("ip", ip),
				slog.String("reason", "source IP not in allowlist"))
			c.ack(w)
			return
		}
	}

	var n dto.Notification
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		// Even a body we cannot parse gets a 200: retrying it will not make it
		// parseable.
		slog.WarnContext(r.Context(), "unparseable payment notification", slog.Any("err", err))
		c.ack(w)
		return
	}

	if err := c.usecase.HandleNotification(r.Context(), n); err != nil {
		slog.WarnContext(r.Context(), "payment notification not applied",
			slog.String("order_id", n.OrderID),
			slog.String("transaction_status", n.TransactionStatus),
			slog.Any("err", err))
	}
	c.ack(w)
}

func (c *PaymentController) ack(w http.ResponseWriter) {
	apinizer.WriteEnvelope(w, http.StatusOK, "Callback processed", struct{}{})
}

// webhookClientIP extracts the client IP from the request, preferring the
// API Gateway context (Lambda) over X-Forwarded-For (which the client controls
// and cannot be trusted when set by anything other than the gateway).
func webhookClientIP(r *http.Request) string {
	// Lambda behind API Gateway: the source IP is in the request context.
	if gw, ok := core.GetAPIGatewayV2ContextFromContext(r.Context()); ok && gw.HTTP.SourceIP != "" {
		return gw.HTTP.SourceIP
	}
	// Fallback for local dev or tests: use RemoteAddr, stripping the port.
	// X-Forwarded-For is deliberately NOT used as a fallback: outside Lambda,
	// it is client-controlled and would let any caller claim any IP.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without a port (rare).
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
