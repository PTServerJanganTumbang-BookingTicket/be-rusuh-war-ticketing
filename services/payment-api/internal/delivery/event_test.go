package delivery

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvoke_APIGatewayEvent_StillRoutesToHTTP(t *testing.T) {
	proxyCalled := false
	proxy := func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		proxyCalled = true
		assert.Equal(t, "POST", req.RequestContext.HTTP.Method)
		return events.APIGatewayV2HTTPResponse{StatusCode: 200}, nil
	}

	h := NewEventHandler(nil, proxy)

	// An API Gateway event has no "op" field
	payload := `{
		"version": "2.0",
		"requestContext": {"http": {"method": "POST", "path": "/payments/webhook"}},
		"body": "{}"
	}`

	resp, err := h.Invoke(context.Background(), []byte(payload))
	require.NoError(t, err)
	assert.True(t, proxyCalled)

	var apiResp events.APIGatewayV2HTTPResponse
	require.NoError(t, json.Unmarshal(resp, &apiResp))
	assert.Equal(t, 200, apiResp.StatusCode)
}

func TestInvoke_UnknownOp_ReturnsAnErrorCode(t *testing.T) {
	h := NewEventHandler(nil, nil)

	payload := `{"op": "some_unknown_operation"}`
	resp, err := h.Invoke(context.Background(), []byte(payload))
	require.NoError(t, err)

	var reply opReply
	require.NoError(t, json.Unmarshal(resp, &reply))
	assert.Equal(t, "UNKNOWN_OP", reply.ErrorCode)
}

// The []uint8 trap: when Lambda sends a JSON payload as json.RawMessage inside
// another struct, it arrives as []byte. If the unmarshaling doesn't handle this,
// the content can be corrupted. This test ensures the JSON object payload
// unmarshals correctly.
func TestInvoke_JSONObjectPayload_Unmarshals(t *testing.T) {
	proxyCalled := false
	proxy := func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		proxyCalled = true
		// Verify the body was correctly passed through
		assert.NotEmpty(t, req.Body)
		return events.APIGatewayV2HTTPResponse{StatusCode: 200, Body: `{"status":"ok"}`}, nil
	}

	h := NewEventHandler(nil, proxy)

	// Simulate what comes through when Lambda deserializes the event
	apiEvent := events.APIGatewayV2HTTPRequest{
		Version: "2.0",
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/payments/webhook",
			},
		},
		Body: `{"order_id":"RB-001:1","transaction_status":"settlement"}`,
	}
	payload, err := json.Marshal(apiEvent)
	require.NoError(t, err)

	resp, err := h.Invoke(context.Background(), payload)
	require.NoError(t, err)
	assert.True(t, proxyCalled)

	var apiResp events.APIGatewayV2HTTPResponse
	require.NoError(t, json.Unmarshal(resp, &apiResp))
	assert.Equal(t, 200, apiResp.StatusCode)
}

func TestInvoke_SweepOpNotConfigured_ReturnsErrorCode(t *testing.T) {
	h := NewEventHandler(nil, nil)

	payload := `{"op": "sweep_awaiting_payment"}`
	resp, err := h.Invoke(context.Background(), []byte(payload))
	require.NoError(t, err)

	var reply opReply
	require.NoError(t, json.Unmarshal(resp, &reply))
	assert.Equal(t, "NOT_CONFIGURED", reply.ErrorCode)
}

func TestInvoke_InvalidJSON_ReturnsError(t *testing.T) {
	h := NewEventHandler(nil, nil)

	payload := `not valid json`
	_, err := h.Invoke(context.Background(), []byte(payload))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unreadable event")
}

// Note: TestInvoke_SweepOp_CallsSweepUsecase is not included because
// SweepUsecase is a concrete type that cannot easily be faked. The sweep
// behavior is tested in usecase/sweep_test.go. Integration testing of the
// event handler with a real sweep is done in delivery server tests.
