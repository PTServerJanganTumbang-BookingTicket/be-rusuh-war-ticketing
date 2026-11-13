// Package midtrans is the Core API client.
//
// Core API rather than Snap: Snap hosts its own payment page, which would take
// the TTL countdown out of our hands and put a redirect in the middle of the
// flow. Core API returns the VA number or QR to us, so the customer never
// leaves the app and the deadline stays ours (ADR-005 §9).
package midtrans

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/valueobject"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// wib is Asia/Jakarta, fixed rather than loaded from the host tzdata: a
// Lambda image without zoneinfo would silently fall back to UTC and shift
// every custom_expiry by seven hours.
var wib = time.FixedZone("WIB", 7*60*60)

// Client talks to Midtrans Core API.
type Client struct {
	http      *http.Client
	baseURL   string
	serverKey string
}

// New builds the client. baseURL is the sandbox or production Core API host.
func New(baseURL, serverKey string, timeout time.Duration) *Client {
	return &Client{
		http:      &http.Client{Timeout: timeout},
		baseURL:   baseURL,
		serverKey: serverKey,
	}
}

// authHeader is HTTP Basic with the server key as the username and an empty
// password. The trailing colon is required.
func (c *Client) authHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.serverKey+":"))
}

type chargeBody struct {
	PaymentType        string             `json:"payment_type"`
	TransactionDetails transactionDetails `json:"transaction_details"`
	BankTransfer       *bankTransfer      `json:"bank_transfer,omitempty"`
	CustomExpiry       *customExpiry      `json:"custom_expiry,omitempty"`
}

type transactionDetails struct {
	OrderID string `json:"order_id"`
	// GrossAmount is an integer here: on the CHARGE request Midtrans accepts a
	// number. It comes BACK on the notification as a decimal string, and that
	// string is what the signature uses — the two are not interchangeable.
	GrossAmount int64 `json:"gross_amount"`
}

type bankTransfer struct {
	Bank string `json:"bank"`
}

// customExpiry makes Midtrans's window end when ours does.
//
// Without it a VA's default expiry is 24 HOURS, so it stays payable long after
// the sweep released the seats and sold them to someone else. Both POC methods
// accept it, which is exactly why cards are excluded: Midtrans states
// explicitly that custom_expiry does not apply to cards, so BR-05 would have
// no gateway-side enforcement at all.
type customExpiry struct {
	OrderTime      string `json:"order_time"`
	ExpiryDuration int    `json:"expiry_duration"`
	Unit           string `json:"unit"`
}

type chargeResponse struct {
	StatusCode        string `json:"status_code"`
	StatusMessage     string `json:"status_message"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	OrderID           string `json:"order_id"`
	PaymentType       string `json:"payment_type"`
	GrossAmount       string `json:"gross_amount"`
	// SettlementTime is when the money moved, in WIB. Present on settlement
	// and refund notifications, absent on pending.
	SettlementTime string `json:"settlement_time"`
	// TransactionTime is when the transaction was created, in WIB. Always
	// present; falls back for paid_at when settlement_time is missing.
	TransactionTime string `json:"transaction_time"`
	VANumbers       []struct {
		Bank     string `json:"bank"`
		VANumber string `json:"va_number"`
	} `json:"va_numbers"`
	Actions []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"actions"`
	// ExpiryTime is when Midtrans will stop accepting payment. Same shape as
	// transaction_time - "2006-01-02 15:04:05" in WIB with no zone marker -
	// so it must be parsed in wib, not UTC.
	ExpiryTime string `json:"expiry_time"`
}

// gatewayExpiryTolerance is how far Midtrans's expiry_time may sit from the
// window we asked for before it is worth a log line.
//
// Not zero: the window is sent to the second, but Midtrans may still round the
// stored value, and a second or two of clock skew between us and the gateway
// proves nothing. A minute of slack keeps the warning for the case it exists
// to catch - custom_expiry clamped to hours, or ignored entirely and reverted
// to the 24-hour default.
const gatewayExpiryTolerance = time.Minute

// Charge creates the payment instrument.
func (c *Client) Charge(ctx context.Context, req portout.ChargeRequest) (portout.ChargeResult, error) {
	body := chargeBody{
		PaymentType:        req.PaymentType,
		TransactionDetails: transactionDetails{OrderID: req.OrderID, GrossAmount: req.GrossAmount},
		CustomExpiry: &customExpiry{
			// held_at in WIB, NOT the request time. A charge sent at 10:23 for
			// a booking held at 10:15 must still expire at 10:25; anchoring on
			// now() would grant a fresh window on every method switch and the
			// payment TTL would never actually elapse.
			OrderTime: req.HeldAt.In(wib).Format("2006-01-02 15:04:05 -0700"),
			// Seconds, which Midtrans accepts alongside minute, hour and day.
			// The alternative truncates: a 7m30s window would be sent as 7
			// minutes and the gateway would refuse payment with thirty seconds
			// still on the customer's clock.
			ExpiryDuration: req.ExpirySeconds,
			Unit:           "second",
		},
	}
	if req.PaymentType == entity.MethodBankTransfer {
		body.BankTransfer = &bankTransfer{Bank: req.Bank}
	}

	raw, err := c.do(ctx, http.MethodPost, "/v2/charge", body)
	if err != nil {
		return portout.ChargeResult{}, err
	}
	var res chargeResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return portout.ChargeResult{}, fmt.Errorf("midtrans: decode charge: %w", err)
	}

	out := portout.ChargeResult{
		TransactionStatus: res.TransactionStatus,
		FraudStatus:       res.FraudStatus,
		StatusCode:        res.StatusCode,
		StatusMessage:     res.StatusMessage,
		Raw:               raw,
		Instructions:      map[string]string{},
	}
	if len(res.VANumbers) > 0 {
		out.Instructions["bank"] = res.VANumbers[0].Bank
		out.Instructions["va_number"] = res.VANumbers[0].VANumber
	}
	// expiry_time is the one check available on the single gateway-side
	// guarantee BR-05 leans on. A mismatch is logged rather than failing the
	// charge: the instrument exists and the customer can pay it, so refusing
	// here would throw away a live payment over a reporting discrepancy.
	if res.ExpiryTime != "" {
		if got, perr := time.ParseInLocation(valueobject.MidtransTimeLayout, res.ExpiryTime, wib); perr == nil {
			out.GatewayExpiry = &got
			want := req.HeldAt.Add(time.Duration(req.ExpirySeconds) * time.Second)
			if drift := got.Sub(want); drift > gatewayExpiryTolerance || drift < -gatewayExpiryTolerance {
				slog.Warn("midtrans expiry_time does not match the window we requested",
					slog.String("order_id", req.OrderID),
					slog.String("payment_type", req.PaymentType),
					slog.Time("requested", want),
					slog.Time("gateway", got),
					slog.Duration("drift", drift))
			}
		} else {
			slog.Warn("midtrans expiry_time is unparseable",
				slog.String("order_id", req.OrderID),
				slog.String("expiry_time", res.ExpiryTime),
				slog.Any("err", perr))
		}
	}

	for _, a := range res.Actions {
		switch a.Name {
		case "generate-qr-code":
			// QRIS. The customer may pay with any QRIS app, which is why the
			// webhook may later arrive as payment_type "qris" rather than
			// "gopay" — and why the handler must not match the two.
			out.Instructions["qr_url"] = a.URL
		case "deeplink-redirect":
			out.Instructions["deeplink_url"] = a.URL
		}
	}
	return out, nil
}

// Cancel voids a pending transaction.
//
// Not Expire: nothing expired when the customer chose another method, the
// merchant cancelled it, and Midtrans reports the two separately on the
// dashboard where someone will eventually have to reconcile them.
func (c *Client) Cancel(ctx context.Context, orderID string) error {
	raw, err := c.do(ctx, http.MethodPost, "/v2/"+orderID+"/cancel", nil)
	if err != nil {
		return err
	}
	var res chargeResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil
	}
	// 412 with a settled transaction is Midtrans refusing to cancel something
	// already paid. That is a real outcome, not a transport failure: §5.3
	// answers 409 PAYMENT_ALREADY_SETTLED and changes nothing.
	if res.TransactionStatus == "settlement" || res.TransactionStatus == "capture" {
		return portout.ErrAlreadySettled
	}
	return nil
}

// Expire ends a pending transaction because its window closed. Called by the
// admission-ctl sweep, not by this service.
func (c *Client) Expire(ctx context.Context, orderID string) error {
	_, err := c.do(ctx, http.MethodPost, "/v2/"+orderID+"/expire", nil)
	return err
}

// Status is the read-only lookup the AWAITING_PAYMENT sweep uses to find out
// what happened to a charge whose webhook never arrived.
func (c *Client) Status(ctx context.Context, orderID string) (portout.StatusResult, error) {
	raw, err := c.do(ctx, http.MethodGet, "/v2/"+orderID+"/status", nil)
	if err != nil {
		return portout.StatusResult{}, err
	}
	var res chargeResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return portout.StatusResult{}, fmt.Errorf("midtrans: decode status: %w", err)
	}
	return portout.StatusResult{
		TransactionStatus: res.TransactionStatus,
		FraudStatus:       res.FraudStatus,
		StatusCode:        res.StatusCode,
		GrossAmount:       res.GrossAmount,
		SettlementTime:    res.SettlementTime,
		TransactionTime:   res.TransactionTime,
		Raw:               raw,
	}, nil
}

// do performs one request.
//
// A non-2xx is returned as an error EXCEPT for the 4xx bodies Midtrans uses to
// report business outcomes (402 declined, 412 cannot modify): those carry a
// status_code the caller must inspect, so the body is handed back.
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", c.authHeader())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Timeout or connection failure. The caller must NOT retry the charge:
		// the instrument may well exist. This is a write dependency.
		return nil, fmt.Errorf("midtrans: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("midtrans: read body: %w", err)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("midtrans: %s %s: upstream %d", method, path, resp.StatusCode)
	}
	if resp.StatusCode >= 400 && !carriesStatusCode(raw) {
		return nil, fmt.Errorf("midtrans: %s %s: %d", method, path, resp.StatusCode)
	}
	return raw, nil
}

func carriesStatusCode(raw []byte) bool {
	var probe struct {
		StatusCode string `json:"status_code"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	if probe.StatusCode == "" {
		return false
	}
	_, err := strconv.Atoi(probe.StatusCode)
	return err == nil
}

// Compile-time proof the client satisfies the port.
var _ portout.IMidtransClient = (*Client)(nil)
