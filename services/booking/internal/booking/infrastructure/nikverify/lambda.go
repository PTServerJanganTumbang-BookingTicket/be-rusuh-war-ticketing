// Package nikverify is booking's way to nik-verify: a direct Lambda invocation (no HTTP route, so the NIK hash can
// never reach a browser; IAM decides who may call), and a local stand-in. Only nik-verify holds NIK_PEPPER.
package nikverify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// invoker is the one call of the Lambda client this adapter makes.
type invoker interface {
	Invoke(ctx context.Context, in *lambda.InvokeInput, opts ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

// Lambda calls the nik-verify function synchronously with the internal event {"op":"validate","nik":"..."}.
type Lambda struct {
	client  invoker
	name    string
	timeout time.Duration
}

// NewLambda builds the adapter. timeout bounds one check, cold start included.
func NewLambda(client invoker, functionName string, timeout time.Duration) *Lambda {
	return &Lambda{client: client, name: functionName, timeout: timeout}
}

// Connect builds the adapter on the default AWS configuration (the Lambda's execution role).
func Connect(ctx context.Context, functionName string, timeout time.Duration) (*Lambda, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("nikverify: aws config: %w", err)
	}
	return NewLambda(lambda.NewFromConfig(cfg), functionName, timeout), nil
}

var _ outbound.NIKVerifier = (*Lambda)(nil)

type validateEvent struct {
	Op  string `json:"op"`
	NIK string `json:"nik"`
}

// validateReply is nik-verify's answer: the check, or an error_code when it could not answer.
type validateReply struct {
	NIKHmac   string `json:"nik_hmac"`
	NIKMasked string `json:"nik_masked"`
	Valid     bool   `json:"valid"`
	Status    string `json:"status"`
	Source    string `json:"source"`
	ErrorCode string `json:"error_code"`
}

// Validate invokes nik-verify. Lambda reports a crashed or timed-out function as HTTP 200 with FunctionError set,
// so that field is checked, never the status alone. Every failure is domain.ErrNIKUnavailable (fail closed), with
// the cause wrapped for the logs; the NIK itself is never logged.
func (l *Lambda) Validate(ctx context.Context, nik string) (domain.NIKCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	payload, err := json.Marshal(validateEvent{Op: "validate", NIK: nik})
	if err != nil {
		return domain.NIKCheck{}, fmt.Errorf("%w: encode event: %v", domain.ErrNIKUnavailable, err)
	}
	out, err := l.client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(l.name),
		InvocationType: lambdatypes.InvocationTypeRequestResponse,
		Payload:        payload,
	})
	if err != nil {
		return domain.NIKCheck{}, fmt.Errorf("%w: invoke: %w", domain.ErrNIKUnavailable, err)
	}
	if out == nil {
		return domain.NIKCheck{}, fmt.Errorf("%w: no output", domain.ErrNIKUnavailable)
	}
	if out.FunctionError != nil {
		return domain.NIKCheck{}, fmt.Errorf("%w: function error %q", domain.ErrNIKUnavailable, *out.FunctionError)
	}
	if out.StatusCode != 200 {
		return domain.NIKCheck{}, fmt.Errorf("%w: status %d", domain.ErrNIKUnavailable, out.StatusCode)
	}
	var r validateReply
	if err := json.Unmarshal(out.Payload, &r); err != nil {
		return domain.NIKCheck{}, fmt.Errorf("%w: unreadable answer: %v", domain.ErrNIKUnavailable, err)
	}
	if r.ErrorCode != "" {
		// DUKCAPIL_UNAVAILABLE: breaker open, rate limit reached, Dukcapil error. VALIDATION_ERROR: booking checked the
		// format first, so this is a bug between the two services. Both are unavailable to the customer.
		slog.Warn("nikverify: nik-verify did not answer", slog.String("error_code", r.ErrorCode))
		return domain.NIKCheck{}, fmt.Errorf("%w: nik-verify answered %s", domain.ErrNIKUnavailable, r.ErrorCode)
	}
	if r.NIKHmac == "" {
		// Without the hash nothing can be stored or claimed.
		return domain.NIKCheck{}, fmt.Errorf("%w: answer without nik_hmac", domain.ErrNIKUnavailable)
	}
	return domain.NIKCheck{NIKHmac: r.NIKHmac, NIKMasked: r.NIKMasked, Valid: r.Valid, Status: r.Status, Source: r.Source}, nil
}
