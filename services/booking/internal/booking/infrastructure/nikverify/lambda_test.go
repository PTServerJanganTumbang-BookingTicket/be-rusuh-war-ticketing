package nikverify

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

// fakeInvoker answers Invoke from a canned reply and records what it was asked.
type fakeInvoker struct {
	out   *lambda.InvokeOutput
	err   error
	block bool // wait for the context instead of answering
	in    *lambda.InvokeInput
	calls int
}

func (f *fakeInvoker) Invoke(ctx context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	f.calls++
	f.in = in
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.out, f.err
}

func answer(status int32, payload string, functionError string) *lambda.InvokeOutput {
	out := &lambda.InvokeOutput{StatusCode: status, Payload: []byte(payload)}
	if functionError != "" {
		out.FunctionError = &functionError
	}
	return out
}

const valid = `{"nik_hmac":"abc123","nik_masked":"3171********0022","valid":true,"status":"ACTIVE","source":"verified"}`

func newAdapter(f *fakeInvoker) *Lambda {
	return NewLambda(f, "rusuh-prod-nik-verify", 3*time.Second)
}

func TestTheAdapterInvokesTheFunctionSynchronouslyWithTheValidateEvent(t *testing.T) {
	f := &fakeInvoker{out: answer(200, valid, "")}
	if _, err := newAdapter(f).Validate(context.Background(), "3171 0000 0000 0022"); err != nil {
		t.Fatal(err)
	}
	if f.in == nil || *f.in.FunctionName != "rusuh-prod-nik-verify" || f.in.InvocationType != lambdatypes.InvocationTypeRequestResponse {
		t.Fatalf("invoke = %+v, want the named function, RequestResponse", f.in)
	}
	var ev map[string]string
	if err := json.Unmarshal(f.in.Payload, &ev); err != nil || len(ev) != 2 || ev["op"] != "validate" || ev["nik"] != "3171 0000 0000 0022" {
		t.Fatalf("event = %s (%v), want exactly {op: validate, nik}", f.in.Payload, err)
	}
}

func TestAValidAnswerCarriesTheHashTheMaskAndTheSource(t *testing.T) {
	c, err := newAdapter(&fakeInvoker{out: answer(200, valid, "")}).Validate(context.Background(), "3171000000000022")
	want := domain.NIKCheck{NIKHmac: "abc123", NIKMasked: "3171********0022", Valid: true, Status: "ACTIVE", Source: "verified"}
	if err != nil || c != want {
		t.Fatalf("check = %+v (%v), want %+v", c, err, want)
	}
}

// "This NIK is not valid" is an answer, not a failure: the customer is told which seat to fix.
func TestARejectedNIKIsAnAnswerNotAnError(t *testing.T) {
	p := `{"nik_hmac":"def456","nik_masked":"3171********0000","valid":false,"status":"NOT_FOUND","source":"verified"}`
	c, err := newAdapter(&fakeInvoker{out: answer(200, p, "")}).Validate(context.Background(), "3171000000000000")
	if err != nil || c.Valid || c.Status != "NOT_FOUND" {
		t.Fatalf("check = %+v (%v), want valid=false NOT_FOUND without an error", c, err)
	}
}

// Every way the call can fail is "unavailable": no hold without proof (fail closed).
func TestEveryFailureOfTheCallIsUnavailable(t *testing.T) {
	cases := map[string]*fakeInvoker{
		"the Invoke API fails":              {err: errors.New("TooManyRequestsException")},
		"a function error (crash, timeout)": {out: answer(200, `{"errorMessage":"boom","errorType":"Runtime.ExitError"}`, "Unhandled")},
		"a status other than 200":           {out: answer(502, "", "")},
		"nik-verify says Dukcapil is down":  {out: answer(200, `{"error_code":"DUKCAPIL_UNAVAILABLE"}`, "")},
		"nik-verify refuses the format":     {out: answer(200, `{"error_code":"VALIDATION_ERROR"}`, "")},
		"a payload that is not JSON":        {out: answer(200, `not json`, "")},
		"an empty payload":                  {out: answer(200, ``, "")},
		"a valid answer without a hash":     {out: answer(200, `{"nik_hmac":"","nik_masked":"x","valid":true,"status":"ACTIVE","source":"verified"}`, "")},
		"no output at all":                  {},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := newAdapter(f).Validate(context.Background(), "3171000000000022")
			if !errors.Is(err, domain.ErrNIKUnavailable) || c != (domain.NIKCheck{}) {
				t.Fatalf("check = %+v err = %v, want ErrNIKUnavailable and no check", c, err)
			}
		})
	}
}

func TestACallThatOutlastsTheDeadlineIsUnavailableAndStopsAtTheDeadline(t *testing.T) {
	a := NewLambda(&fakeInvoker{block: true}, "f", 50*time.Millisecond)
	start := time.Now()
	_, err := a.Validate(context.Background(), "3171000000000022")
	if !errors.Is(err, domain.ErrNIKUnavailable) {
		t.Fatalf("err = %v, want ErrNIKUnavailable", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v, want about the 50 ms deadline", d)
	}
}

func TestACancelledRequestIsUnavailableToo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewLambda(&fakeInvoker{block: true}, "f", time.Second).Validate(ctx, "3171000000000022"); !errors.Is(err, domain.ErrNIKUnavailable) {
		t.Fatalf("err = %v, want ErrNIKUnavailable", err)
	}
}

func TestTheUnderlyingCauseIsKeptForTheLogs(t *testing.T) {
	cause := errors.New("TooManyRequestsException")
	_, err := newAdapter(&fakeInvoker{err: cause}).Validate(context.Background(), "3171000000000022")
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want it to wrap the cause", err)
	}
}
