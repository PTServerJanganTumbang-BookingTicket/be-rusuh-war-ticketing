// Package dukcapil adapts the national identity verification API.
package dukcapil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/outbound"
)

// Client calls the real Dukcapil HTTP API.
type Client struct {
	baseURL      string
	apiKey       string
	extraHeaders map[string]string
	http         *http.Client
}

// NewClient builds a Dukcapil client with a hard timeout.
//
// The timeout is not optional. Without it a hung government endpoint holds a
// Lambda invocation until the platform kills it, and the circuit breaker never
// sees a failure to record because nothing ever returns.
// extraHeaders is sent on every request in addition to the two below, and may
// be nil. It exists for whatever sits in front of Dukcapil in a given
// environment - a mock server, an API gateway, a corporate proxy - each of
// which authenticates the caller its own way. Those header names are not part
// of the Dukcapil contract, so they do not get fields of their own here.
func NewClient(baseURL, apiKey string, timeout time.Duration, extraHeaders map[string]string) portout.IDukcapilAdapter {
	return &Client{
		baseURL:      baseURL,
		apiKey:       apiKey,
		extraHeaders: extraHeaders,
		http:         &http.Client{Timeout: timeout},
	}
}

type verifyRequest struct {
	// The only field. Adding name or date of birth here would breach FR-15a and
	// requirements §12, which state those are neither collected nor sent.
	NIK string `json:"nik"`
}

type verifyResponse struct {
	Valid  bool   `json:"valid"`
	Status string `json:"status"`
	Ref    string `json:"reference_id"`
}

// VerifyNIK sends the NIK and interprets the answer.
//
// Returns an error only when no answer was obtained. A NIK that Dukcapil
// rejects comes back as a result with Valid false, because that is a working
// dependency giving a negative answer and must not trip the breaker.
func (c *Client) VerifyNIK(ctx context.Context, nik string) (portout.DukcapilResult, error) {
	body, err := json.Marshal(verifyRequest{NIK: nik})
	if err != nil {
		return portout.DukcapilResult{}, fmt.Errorf("dukcapil: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/verify", bytes.NewReader(body))
	if err != nil {
		return portout.DukcapilResult{}, fmt.Errorf("dukcapil: build request: %w", err)
	}
	// Extras first: the two below are the contract and must win a collision.
	for name, value := range c.extraHeaders {
		req.Header.Set(name, value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return portout.DukcapilResult{}, fmt.Errorf("dukcapil: call failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		// Dukcapil answered: this NIK does not exist. Not an outage.
		return portout.DukcapilResult{Valid: false, Status: "NOT_FOUND"}, nil
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests:
		return portout.DukcapilResult{}, fmt.Errorf("dukcapil: upstream status %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return portout.DukcapilResult{}, fmt.Errorf("dukcapil: unexpected status %d", resp.StatusCode)
	}

	var parsed verifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return portout.DukcapilResult{}, fmt.Errorf("dukcapil: decode response: %w", err)
	}
	if parsed.Status == "" {
		parsed.Status = "ACTIVE"
	}
	return portout.DukcapilResult{Valid: parsed.Valid, Status: parsed.Status, Ref: parsed.Ref}, nil
}
