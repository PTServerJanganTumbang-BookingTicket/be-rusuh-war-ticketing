package delivery_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/stretchr/testify/assert"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/delivery"
)

// v2Event builds a minimal API Gateway HTTP API (payload v2) request.
func v2Event(method, path, body string) events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{
		RawPath: path,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method: method,
				Path:   path,
			},
		},
		Body: body,
	}
}

func TestLambdaProxy_CreateThenGetBooking(t *testing.T) {
	adapter := httpadapter.NewV2(delivery.New(config.Config{}))

	// POST /bookings
	createBody := `{
		"event_id": "evt_rusuh_2026",
		"tickets": [{"category_id": "cat_vip", "nik": "3171000000000001", "holder_email": "a@example.com"}],
		"contact_email": "contact@example.com",
		"terms_version": "v1"
	}`
	createResp, err := adapter.ProxyWithContext(context.Background(), v2Event("POST", "/bookings", createBody))
	assert.NoError(t, err)
	assert.Equal(t, 201, createResp.StatusCode)

	var created struct {
		Meta struct {
			Code int `json:"code"`
		} `json:"meta"`
		Data struct {
			BookingID string `json:"booking_id"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	assert.NoError(t, json.Unmarshal([]byte(createResp.Body), &created))
	assert.Equal(t, 201, created.Meta.Code)
	assert.NotEmpty(t, created.Data.BookingID)
	assert.Equal(t, "PENDING_PAYMENT", created.Data.Status)

	// GET /bookings/{booking_id}
	getResp, err := adapter.ProxyWithContext(context.Background(), v2Event("GET", "/bookings/"+created.Data.BookingID, ""))
	assert.NoError(t, err)
	assert.Equal(t, 200, getResp.StatusCode)

	var got struct {
		Data struct {
			BookingID string `json:"booking_id"`
		} `json:"data"`
	}
	assert.NoError(t, json.Unmarshal([]byte(getResp.Body), &got))
	assert.Equal(t, created.Data.BookingID, got.Data.BookingID)
}

func TestLambdaProxy_HealthCheck(t *testing.T) {
	adapter := httpadapter.NewV2(delivery.New(config.Config{}))

	resp, err := adapter.ProxyWithContext(context.Background(), v2Event("GET", "/sys/ping", ""))
	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}
