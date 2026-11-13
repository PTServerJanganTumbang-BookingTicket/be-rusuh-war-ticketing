// Package lambda holds adapters that invoke other services via Lambda.
package lambda

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/port/outbound"
)

// opSweepAwaiting is the operation code for the backstop sweep.
const opSweepAwaiting = "sweep_awaiting_payment"

// Invoker is the subset of the Lambda client used by this adapter.
type Invoker interface {
	Invoke(ctx context.Context, params *lambda.InvokeInput, optFns ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

// PaymentReconciler invokes the payment-api Lambda to sweep AWAITING_PAYMENT
// bookings whose webhook never arrived.
type PaymentReconciler struct {
	client       Invoker
	functionName string
}

// Compile-time interface check.
var _ outbound.PaymentReconciler = (*PaymentReconciler)(nil)

// NewPaymentReconciler creates a reconciler that invokes the given function.
func NewPaymentReconciler(client Invoker, functionName string) *PaymentReconciler {
	return &PaymentReconciler{client: client, functionName: functionName}
}

// sweepRequest is the payload sent to payment-api.
type sweepRequest struct {
	Op string `json:"op"`
}

// sweepResponse is the payload payment-api returns.
type sweepResponse struct {
	ErrorCode string `json:"error_code,omitempty"`
	Expired   int    `json:"expired,omitempty"`
	Settled   int    `json:"settled,omitempty"`
	Skipped   int    `json:"skipped,omitempty"`
}

// SweepAwaitingPayment invokes {"op":"sweep_awaiting_payment"} on payment-api.
func (p *PaymentReconciler) SweepAwaitingPayment(ctx context.Context) (domain.SweepResult, error) {
	payload, err := json.Marshal(sweepRequest{Op: opSweepAwaiting})
	if err != nil {
		return domain.SweepResult{}, fmt.Errorf("marshal sweep request: %w", err)
	}

	out, err := p.client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(p.functionName),
		InvocationType: types.InvocationTypeRequestResponse,
		Payload:        payload,
	})
	if err != nil {
		return domain.SweepResult{}, fmt.Errorf("invoke payment-api: %w", err)
	}

	// Lambda errors are reported in FunctionError, not as an SDK error.
	if out.FunctionError != nil {
		return domain.SweepResult{}, fmt.Errorf("payment-api returned error: %s", *out.FunctionError)
	}

	var resp sweepResponse
	if err := json.Unmarshal(out.Payload, &resp); err != nil {
		return domain.SweepResult{}, fmt.Errorf("unmarshal sweep response: %w", err)
	}
	if resp.ErrorCode != "" {
		return domain.SweepResult{}, fmt.Errorf("payment-api error: %s", resp.ErrorCode)
	}

	return domain.SweepResult{
		Expired: resp.Expired,
		Settled: resp.Settled,
		Skipped: resp.Skipped,
	}, nil
}
