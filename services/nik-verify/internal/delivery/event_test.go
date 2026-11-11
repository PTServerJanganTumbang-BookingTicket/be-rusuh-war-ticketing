package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/dto"
)

// fakeUsecase stands in for the one NIK use case both entry points call.
type fakeUsecase struct {
	resp  dto.ValidateResponse
	err   error
	calls []dto.ValidateRequest
}

func (f *fakeUsecase) Validate(_ context.Context, req dto.ValidateRequest) (dto.ValidateResponse, error) {
	f.calls = append(f.calls, req)
	return f.resp, f.err
}

// fakeProxy stands in for the API Gateway adapter in front of the HTTP routes.
type fakeProxy struct {
	got  []events.APIGatewayV2HTTPRequest
	resp events.APIGatewayV2HTTPResponse
	err  error
}

func (f *fakeProxy) proxy(_ context.Context, r events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	f.got = append(f.got, r)
	return f.resp, f.err
}

var valid = dto.ValidateResponse{NIKMasked: "3171**********22", NIKHmac: "abc123", Valid: true, Status: "ACTIVE", Source: "verified"}

func invoke(t *testing.T, uc *fakeUsecase, p *fakeProxy, payload string) (map[string]any, error) {
	t.Helper()
	out, err := NewEventHandler(uc, p.proxy).Invoke(context.Background(), []byte(payload))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("reply is not JSON: %s", out)
	}
	return m, nil
}

func TestTheInternalValidateEventAnswersWithTheHashTheMaskAndTheVerdict(t *testing.T) {
	uc := &fakeUsecase{resp: valid}
	got, err := invoke(t, uc, &fakeProxy{}, `{"op":"validate","nik":"3171 0000 0000 0022"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"nik_hmac": "abc123", "nik_masked": "3171**********22", "valid": true, "status": "ACTIVE", "source": "verified"}
	if len(got) != len(want) {
		t.Fatalf("reply = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, want %v", k, got[k], v)
		}
	}
	if len(uc.calls) != 1 || uc.calls[0].NIK != "3171 0000 0000 0022" {
		t.Fatalf("use case calls = %+v, want the NIK exactly as sent (the use case normalises it)", uc.calls)
	}
}

// Same use case as POST /nik/validate: the breaker, the rate limit and the cache apply to this path too.
func TestAnInvalidNIKIsStillAnAnswerNotAnError(t *testing.T) {
	uc := &fakeUsecase{resp: dto.ValidateResponse{NIKMasked: "m", NIKHmac: "h", Valid: false, Status: "NOT_FOUND", Source: "verified"}}
	got, err := invoke(t, uc, &fakeProxy{}, `{"op":"validate","nik":"3171000000000000"}`)
	if err != nil || got["valid"] != false || got["status"] != "NOT_FOUND" || got["error_code"] != nil {
		t.Fatalf("reply = %v (%v), want valid=false NOT_FOUND without an error_code", got, err)
	}
}

func TestEveryUseCaseFailureIsAnErrorCodeNeverAnAnswer(t *testing.T) {
	cases := map[string]struct {
		err  error
		code string
	}{
		"dukcapil unavailable": {apperror.ErrServiceUnavailable("down").WithCode(apperror.CodeDukcapilUnavailable), "DUKCAPIL_UNAVAILABLE"},
		"bad format":           {apperror.ErrBadRequest("16 digits").WithCode(apperror.CodeValidationError), "VALIDATION_ERROR"},
		"an unexpected error":  {errors.New("boom"), "INTERNAL_ERROR"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := invoke(t, &fakeUsecase{err: c.err}, &fakeProxy{}, `{"op":"validate","nik":"3171000000000022"}`)
			if err != nil || got["error_code"] != c.code || got["nik_hmac"] != nil || got["valid"] != nil {
				t.Fatalf("reply = %v (%v), want only error_code %s", got, err, c.code)
			}
		})
	}
}

func TestAnEventWithoutANIKIsAValidationErrorAndNeverReachesTheUseCase(t *testing.T) {
	uc := &fakeUsecase{resp: valid}
	for _, payload := range []string{`{"op":"validate"}`, `{"op":"validate","nik":""}`, `{"op":"validate","nik":12345}`} {
		got, err := invoke(t, uc, &fakeProxy{}, payload)
		if err != nil || got["error_code"] != "VALIDATION_ERROR" {
			t.Fatalf("%s: reply = %v (%v)", payload, got, err)
		}
	}
	if len(uc.calls) != 0 {
		t.Fatal("a call without a NIK must not cost a use case call")
	}
}

func TestAnUnknownOperationIsRefused(t *testing.T) {
	uc := &fakeUsecase{resp: valid}
	got, err := invoke(t, uc, &fakeProxy{}, `{"op":"hash","nik":"3171000000000022"}`)
	if err != nil || got["error_code"] != "UNKNOWN_OP" || len(uc.calls) != 0 {
		t.Fatalf("reply = %v (%v) calls=%d", got, err, len(uc.calls))
	}
}

// An API Gateway event keeps going to the HTTP routes, byte for byte as before.
func TestAnAPIGatewayEventIsForwardedToTheHTTPRoutesAndItsResponseReturned(t *testing.T) {
	p := &fakeProxy{resp: events.APIGatewayV2HTTPResponse{StatusCode: 200, Body: `{"meta":{"code":200}}`}}
	uc := &fakeUsecase{resp: valid}
	event := `{"version":"2.0","routeKey":"POST /nik/validate","rawPath":"/nik/validate","body":"{\"nik\":\"3171000000000022\"}","requestContext":{"http":{"method":"POST"}}}`
	got, err := invoke(t, uc, p, event)
	if err != nil || got["statusCode"] != float64(200) || got["body"] != `{"meta":{"code":200}}` {
		t.Fatalf("reply = %v (%v)", got, err)
	}
	if len(p.got) != 1 || p.got[0].RawPath != "/nik/validate" || len(uc.calls) != 0 {
		t.Fatalf("proxied %+v, use case calls %d, want the event forwarded and the internal path untouched", p.got, len(uc.calls))
	}
}

// A browser cannot reach the internal path: API Gateway builds the event itself, so an "op" in the HTTP body never sits at
// the top level of the payload.
func TestAnOpInsideAnHTTPBodyDoesNotTriggerTheInternalPath(t *testing.T) {
	p := &fakeProxy{resp: events.APIGatewayV2HTTPResponse{StatusCode: 400}}
	uc := &fakeUsecase{resp: valid}
	event := `{"version":"2.0","routeKey":"POST /nik/validate","rawPath":"/nik/validate","body":"{\"op\":\"validate\",\"nik\":\"3171000000000022\"}"}`
	if _, err := invoke(t, uc, p, event); err != nil {
		t.Fatal(err)
	}
	if len(uc.calls) != 0 || len(p.got) != 1 {
		t.Fatalf("use case calls %d, proxied %d: the event must go to the HTTP routes", len(uc.calls), len(p.got))
	}
}

func TestAPayloadThatIsNotJSONIsAFunctionErrorAndNothingRuns(t *testing.T) {
	uc, p := &fakeUsecase{}, &fakeProxy{}
	if _, err := NewEventHandler(uc, p.proxy).Invoke(context.Background(), []byte(`not json`)); err == nil {
		t.Fatal("want an error: Lambda reports it as a function error, which booking treats as unavailable")
	}
	if len(uc.calls) != 0 || len(p.got) != 0 {
		t.Fatal("nothing may run for an unreadable payload")
	}
}

func TestTheProxyFailingIsReturnedAsAnError(t *testing.T) {
	p := &fakeProxy{err: errors.New("proxy down")}
	_, err := NewEventHandler(&fakeUsecase{}, p.proxy).Invoke(context.Background(), []byte(`{"version":"2.0","rawPath":"/sys/ping"}`))
	if err == nil || !strings.Contains(err.Error(), "proxy down") {
		t.Fatalf("err = %v", err)
	}
}

// The real API Gateway adapter in front of the real mux: an HTTP API event still reaches the public routes, and the
// internal event on the same entry point reaches the use case.
func TestTheServersEntryPointServesBothKindsOfEvent(t *testing.T) {
	mux := http.NewServeMux()
	apinizer.RegisterHealthCheck(mux, "/sys/ping")
	uc := &fakeUsecase{resp: valid}
	h := (&Server{Handler: mux, uc: uc}).Events()

	out, err := h.Invoke(context.Background(), []byte(`{"version":"2.0","routeKey":"GET /sys/ping","rawPath":"/sys/ping",
		"requestContext":{"http":{"method":"GET","path":"/sys/ping"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp events.APIGatewayV2HTTPResponse
	if err := json.Unmarshal(out, &resp); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP event: %s (%v), want 200 from the public route", out, err)
	}

	out, err = h.Invoke(context.Background(), []byte(`{"op":"validate","nik":"3171000000000022"}`))
	if err != nil || !strings.Contains(string(out), `"nik_hmac":"abc123"`) || len(uc.calls) != 1 {
		t.Fatalf("internal event: %s (%v) calls=%d", out, err, len(uc.calls))
	}
}

// ── Regression: lambda.StartHandlerFunc vs lambda.Start ───────────────────
//
// lambda.StartHandlerFunc is generic-based. For a handler with signature
// func(context.Context, []byte) ([]byte, error) it instantiates TIn=[]byte
// and calls json.Unmarshal(eventJSON, &[]byte{}) before the handler runs.
// A JSON object cannot be unmarshalled into []byte, so EVERY API Gateway
// request failed with:
//
//   json: cannot unmarshal object into Go value of type []uint8
//
// lambda.Start uses reflection and special-cases []byte: it passes the raw
// JSON bytes directly to the handler without any unmarshalling step.
//
// These tests protect that contract by calling Invoke with realistic API
// Gateway HTTP API v2 payloads that would have caused the runtime to panic
// under StartHandlerFunc.

// TestInvokeHandlesAFullAPIGatewayHTTPAPIv2EventWithoutError checks that
// Invoke receives the raw JSON bytes of an API Gateway event and routes it to
// the proxy — the scenario that broke under lambda.StartHandlerFunc.
func TestInvokeHandlesAFullAPIGatewayHTTPAPIv2EventWithoutError(t *testing.T) {
	p := &fakeProxy{resp: events.APIGatewayV2HTTPResponse{StatusCode: http.StatusOK, Body: `{}`}}
	// A realistic, complete API Gateway HTTP API v2 event matching exactly
	// what the runtime delivers after the lambda.Start fix.
	payload := `{
		"version": "2.0",
		"routeKey": "POST /nik/validate",
		"rawPath": "/nik/validate",
		"rawQueryString": "",
		"headers": {
			"content-type": "application/json",
			"x-forwarded-for": "203.0.113.1",
			"x-forwarded-port": "443",
			"x-forwarded-proto": "https"
		},
		"requestContext": {
			"accountId": "699475956139",
			"apiId": "abc123",
			"domainName": "abc123.execute-api.ap-southeast-1.amazonaws.com",
			"http": {
				"method": "POST",
				"path": "/nik/validate",
				"protocol": "HTTP/1.1",
				"sourceIp": "203.0.113.1",
				"userAgent": "rusuh-user/1.0"
			},
			"stage": "$default"
		},
		"body": "{\"nik\":\"3171000000000022\"}",
		"isBase64Encoded": false
	}`

	_, err := NewEventHandler(&fakeUsecase{resp: valid}, p.proxy).Invoke(context.Background(), []byte(payload))
	if err != nil {
		// If this fails with "cannot unmarshal object into go value of type
		// []uint8" it means the caller is using lambda.StartHandlerFunc.
		t.Fatalf("Invoke must not return an error for a valid API Gateway event: %v", err)
	}
	if len(p.got) != 1 {
		t.Fatalf("proxy called %d times, want 1 — event was not forwarded", len(p.got))
	}
	if p.got[0].RawPath != "/nik/validate" {
		t.Fatalf("proxy received path %q, want /nik/validate", p.got[0].RawPath)
	}
}

// TestInvokeDistinguishesBothEventTypesInOneContainer verifies that the same
// Invoke function dispatches correctly to both paths in a single warm container
// — the main reason the two-payload design exists.
func TestInvokeDistinguishesBothEventTypesInOneContainer(t *testing.T) {
	uc := &fakeUsecase{resp: valid}
	p := &fakeProxy{resp: events.APIGatewayV2HTTPResponse{StatusCode: http.StatusOK}}
	h := NewEventHandler(uc, p.proxy)

	// 1. API Gateway event → proxy, not use case.
	apigwPayload := `{"version":"2.0","routeKey":"POST /nik/validate","rawPath":"/nik/validate",` +
		`"requestContext":{"http":{"method":"POST"}},"body":"{\"nik\":\"3171000000000022\"}"}`
	if _, err := h.Invoke(context.Background(), []byte(apigwPayload)); err != nil {
		t.Fatalf("API Gateway event: %v", err)
	}
	if len(p.got) != 1 || len(uc.calls) != 0 {
		t.Fatalf("after API Gateway event: proxy=%d (want 1) uc=%d (want 0)", len(p.got), len(uc.calls))
	}

	// 2. Internal event → use case, not proxy.
	internalPayload := `{"op":"validate","nik":"3171000000000022"}`
	if _, err := h.Invoke(context.Background(), []byte(internalPayload)); err != nil {
		t.Fatalf("internal event: %v", err)
	}
	if len(p.got) != 1 || len(uc.calls) != 1 {
		t.Fatalf("after internal event: proxy=%d (want 1) uc=%d (want 1)", len(p.got), len(uc.calls))
	}
}
