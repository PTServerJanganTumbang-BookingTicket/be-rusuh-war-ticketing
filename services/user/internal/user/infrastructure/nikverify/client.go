// Package nikverify adapts the nik-verify service's POST /nik/validate.
package nikverify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// Client calls nik-verify over HTTP.
//
// This is the only path from this service to a NIK verification. nik-verify
// owns the Dukcapil rate limiter, the circuit breaker that pauses admission
// (BR-13a) and the 24-hour cache; calling Dukcapil directly from here would
// defeat all three and would require holding the pepper.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds the adapter with a hard timeout.
//
// The timeout must be shorter than the caller's own deadline: nik-verify
// already bounds its Dukcapil call, and stacking an unbounded wait on top of a
// bounded one just moves the hang here.
func NewClient(baseURL string, timeout time.Duration) portout.INikVerifier {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

type validateRequest struct {
	NIK string `json:"nik"`
}

type validateEnvelope struct {
	Meta struct {
		Code      int    `json:"code"`
		Message   string `json:"message"`
		ErrorCode string `json:"error_code"`
	} `json:"meta"`
	Data struct {
		NIKMasked string `json:"nik_masked"`
		NIKHmac   string `json:"nik_hmac"`
		Valid     bool   `json:"valid"`
		Status    string `json:"status"`
		Source    string `json:"source"`
	} `json:"data"`
}

// VerifyNIK asks nik-verify about a NIK.
//
// An error means NO ANSWER was obtainable, and callers fail closed on it. A NIK
// that Dukcapil rejects arrives as HTTP 200 with valid:false and is returned as
// a result, not an error — conflating the two would make a mistyped NIK look
// like an outage and vice versa.
func (c *Client) VerifyNIK(ctx context.Context, nik string) (portout.NIKVerification, error) {
	body, err := json.Marshal(validateRequest{NIK: nik})
	if err != nil {
		return portout.NIKVerification{}, fmt.Errorf("nikverify: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/nik/validate", bytes.NewReader(body))
	if err != nil {
		return portout.NIKVerification{}, fmt.Errorf("nikverify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return portout.NIKVerification{}, fmt.Errorf("nikverify: call failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// 503 DUKCAPIL_UNAVAILABLE, 500, a gateway error — all mean the same
		// thing here: no answer, so fail closed.
		return portout.NIKVerification{}, fmt.Errorf("nikverify: status %d", resp.StatusCode)
	}

	var env validateEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return portout.NIKVerification{}, fmt.Errorf("nikverify: decode response: %w", err)
	}
	if env.Data.Valid && env.Data.NIKHmac == "" {
		// A positive answer with no hash cannot be stored, and storing a NIK
		// with an empty hash would collide with every other such row.
		return portout.NIKVerification{}, fmt.Errorf("nikverify: valid response carried no nik_hmac")
	}

	return portout.NIKVerification{
		NIKHmac:   env.Data.NIKHmac,
		NIKMasked: env.Data.NIKMasked,
		Valid:     env.Data.Valid,
		Status:    env.Data.Status,
		Source:    env.Data.Source,
	}, nil
}
