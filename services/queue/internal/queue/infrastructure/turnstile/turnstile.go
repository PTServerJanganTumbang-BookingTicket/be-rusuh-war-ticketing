// Package turnstile is the Captcha adapter for Cloudflare Turnstile
// (invisible mode, server-side siteverify only).
package turnstile

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/port/outbound"
)

// DefaultURL is Cloudflare's siteverify endpoint.
const DefaultURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// Client verifies tokens with siteverify.
type Client struct {
	url     string
	secret  string
	http    *http.Client
	metrics outbound.Metrics
}

// New builds a client. timeout bounds the whole call (1 s in production).
func New(siteverifyURL, secret string, timeout time.Duration, m outbound.Metrics) *Client {
	if siteverifyURL == "" {
		siteverifyURL = DefaultURL
	}
	return &Client{url: siteverifyURL, secret: secret, http: &http.Client{Timeout: timeout}, metrics: m}
}

type siteverifyResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

// Verify returns Invalid only for an explicit rejection of the customer's
// token. Timeouts, non-200s, bad bodies and our own misconfiguration are
// Unavailable: join fails open on those.
func (c *Client) Verify(ctx context.Context, captchaToken, remoteIP string) domain.CaptchaResult {
	form := url.Values{"secret": {c.secret}, "response": {captchaToken}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(form.Encode()))
	if err != nil {
		return domain.CaptchaUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.CaptchaUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return domain.CaptchaUnavailable
	}
	var body siteverifyResponse
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10)).Decode(&body); err != nil {
		return domain.CaptchaUnavailable
	}
	if body.Success {
		return domain.CaptchaValid
	}
	for _, code := range body.ErrorCodes {
		switch code {
		case "missing-input-secret", "invalid-input-secret":
			// Our secret is wrong: rejecting would block every customer.
			c.metrics.Incr(domain.MetricCaptchaMisconfigured)
			slog.ErrorContext(ctx, "turnstile: secret rejected by siteverify, failing open", slog.String("error_code", code))
			return domain.CaptchaUnavailable
		case "internal-error":
			return domain.CaptchaUnavailable
		}
	}
	return domain.CaptchaInvalid
}

// Mock accepts every token except those in Reject and never calls out.
// For tests and k6 load tests only; config forbids it in production.
type Mock struct {
	Reject map[string]bool
}

// Verify implements outbound.Captcha.
func (m Mock) Verify(_ context.Context, captchaToken, _ string) domain.CaptchaResult {
	if m.Reject[captchaToken] {
		return domain.CaptchaInvalid
	}
	return domain.CaptchaValid
}
