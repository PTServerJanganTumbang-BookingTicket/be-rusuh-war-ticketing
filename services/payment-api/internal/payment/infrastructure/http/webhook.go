package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

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
//     fail again. The charge reconciler is the backstop instead.
//
// Everything interesting is therefore in the log, not the response.
func (c *PaymentController) Webhook(w http.ResponseWriter, r *http.Request) {
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
