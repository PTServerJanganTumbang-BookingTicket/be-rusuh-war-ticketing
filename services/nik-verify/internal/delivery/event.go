package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-lambda-go/events"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/inbound"
)

// opValidate is the one internal operation: check a typed NIK and return its hash.
const opValidate = "validate"

// httpProxy answers an API Gateway HTTP API (v2) event with the existing HTTP routes.
type httpProxy func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error)

// EventHandler is the Lambda entry point. It takes two kinds of payload:
//
//   - an API Gateway event (what `POST /nik/validate` arrives as), forwarded to the HTTP routes exactly as before;
//   - the internal event {"op":"validate","nik":"..."} that booking invokes directly (docs/05-booking/02 §3.1 step 4).
//     It runs the same use case as the public route (rate limit, breaker, cache, fail closed) and answers with the
//     hash and the mask, which the public contract does not promise. There is no route for it: the only way in is
//     lambda:InvokeFunction, which IAM grants to booking alone. A browser cannot forge it, because API Gateway builds the
//     event itself and an "op" inside an HTTP body never sits at the top level of the payload.
type EventHandler struct {
	uc    inbound.INikUsecase
	proxy httpProxy
}

// NewEventHandler wires the use case and the HTTP adapter.
func NewEventHandler(uc inbound.INikUsecase, proxy httpProxy) *EventHandler {
	return &EventHandler{uc: uc, proxy: proxy}
}

// internalReply is the failure shape of the internal event: an error_code and nothing else. A caller treats any error_code
// as "no answer" and fails closed.
type internalReply struct {
	ErrorCode string `json:"error_code"`
}

// Invoke routes one Lambda payload. It never logs the NIK.
func (h *EventHandler) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	var probe struct {
		Op *string `json:"op"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return nil, fmt.Errorf("unreadable event: %w", err)
	}
	if probe.Op == nil {
		return h.forward(ctx, payload)
	}
	if *probe.Op != opValidate {
		return reply(internalReply{ErrorCode: "UNKNOWN_OP"})
	}
	return h.validate(ctx, payload)
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

func (h *EventHandler) validate(ctx context.Context, payload []byte) ([]byte, error) {
	var ev struct {
		NIK string `json:"nik"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil || ev.NIK == "" {
		return reply(internalReply{ErrorCode: apperror.CodeValidationError})
	}
	resp, err := h.uc.Validate(ctx, dto.ValidateRequest{NIK: ev.NIK})
	if err != nil {
		var appErr *apperror.AppError
		if errors.As(err, &appErr) && appErr.ErrorCode != "" {
			return reply(internalReply{ErrorCode: appErr.ErrorCode})
		}
		slog.ErrorContext(ctx, "internal validate failed", slog.Any("err", err))
		return reply(internalReply{ErrorCode: "INTERNAL_ERROR"})
	}
	return reply(resp)
}

func reply(v any) ([]byte, error) { return json.Marshal(v) }
