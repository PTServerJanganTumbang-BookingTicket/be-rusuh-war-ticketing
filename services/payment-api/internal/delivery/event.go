package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/aws/aws-lambda-go/events"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/application/usecase"
)

// opSweepAwaiting is the internal operation for the backstop sweep.
const opSweepAwaiting = "sweep_awaiting_payment"

// httpProxy answers an API Gateway HTTP API (v2) event with the existing HTTP routes.
type httpProxy func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error)

// EventHandler is the Lambda entry point. It takes two kinds of payload:
//
//   - an API Gateway event (what `POST /payments/webhook` arrives as), forwarded
//     to the HTTP routes exactly as before;
//   - the internal event {"op":"sweep_awaiting_payment"} that admission-ctl
//     invokes directly. It runs the backstop sweep for AWAITING_PAYMENT bookings
//     whose webhook never arrived.
//
// There is no HTTP route for the internal op: the only way in is
// lambda:InvokeFunction, which IAM grants to admission-ctl alone.
type EventHandler struct {
	sweep *usecase.SweepUsecase
	proxy httpProxy
}

// NewEventHandler wires the sweep use case and the HTTP adapter.
func NewEventHandler(sweep *usecase.SweepUsecase, proxy httpProxy) *EventHandler {
	return &EventHandler{sweep: sweep, proxy: proxy}
}

// opReply is the response shape for internal operations.
type opReply struct {
	ErrorCode string `json:"error_code,omitempty"`
	Expired   int    `json:"expired,omitempty"`
	Settled   int    `json:"settled,omitempty"`
	Skipped   int    `json:"skipped,omitempty"`
}

// Invoke routes one Lambda payload.
func (h *EventHandler) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	var probe struct {
		Op *string `json:"op"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return nil, fmt.Errorf("unreadable event: %w", err)
	}
	if probe.Op == nil {
		// No op field: assume it's an API Gateway event.
		return h.forward(ctx, payload)
	}
	switch *probe.Op {
	case opSweepAwaiting:
		return h.sweepAwaiting(ctx)
	default:
		return reply(opReply{ErrorCode: "UNKNOWN_OP"})
	}
}

func (h *EventHandler) forward(ctx context.Context, payload []byte) ([]byte, error) {
	var req events.APIGatewayV2HTTPRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("unreadable API Gateway event: %w", err)
	}
	resp, err := h.proxy(ctx, req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resp)
}

func (h *EventHandler) sweepAwaiting(ctx context.Context) ([]byte, error) {
	if h.sweep == nil {
		slog.ErrorContext(ctx, "sweep_awaiting_payment invoked but sweep use case not wired")
		return reply(opReply{ErrorCode: "NOT_CONFIGURED"})
	}

	result, err := h.sweep.SweepAwaitingPayment(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "sweep_awaiting_payment failed", slog.Any("err", err))
		return reply(opReply{ErrorCode: "INTERNAL_ERROR"})
	}

	slog.InfoContext(ctx, "sweep_awaiting_payment completed",
		slog.Int("expired", result.Expired),
		slog.Int("settled", result.Settled),
		slog.Int("skipped", result.Skipped))

	return reply(opReply{
		Expired: result.Expired,
		Settled: result.Settled,
		Skipped: result.Skipped,
	})
}

func reply(v any) ([]byte, error) { return json.Marshal(v) }
