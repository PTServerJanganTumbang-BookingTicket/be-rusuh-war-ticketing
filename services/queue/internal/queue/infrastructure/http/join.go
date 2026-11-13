// Package http is the queue's HTTP adapter (API Gateway Lambda and local server).
package http

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/awslabs/aws-lambda-go-api-proxy/core"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/domain"
)

// HeaderQueueToken carries the queue_token on every client (web and mobile). The queue never sets or reads a cookie:
// the token is in the response body and the client sends it back here.
const HeaderQueueToken = "X-Queue-Token"

// maxBody is far above {"captcha_token": "..."}; larger bodies are not parsed.
const maxBody = 8 << 10

// Joiner is the join use case.
type Joiner interface {
	Join(ctx context.Context, in usecase.JoinInput) (usecase.JoinResult, error)
}

// MeGetter is the GET /queue/me use case.
type MeGetter interface {
	Get(ctx context.Context, in usecase.MeInput) (usecase.MeResult, error)
}

// ServingGetter is the GET /queue/serving use case.
type ServingGetter interface {
	Get(ctx context.Context, eventID string) (usecase.ServingResult, error)
}

// Options configures the handler.
type Options struct {
	SSEBaseURL string // e.g. https://events.my-rusuh.my.id; "" = relative path
}

// Handler serves POST /events/{event_id}/queue/join.
type Handler struct {
	join    Joiner
	me      MeGetter
	serving ServingGetter
	opt     Options
}

// NewHandler builds the handler.
func NewHandler(j Joiner, opt Options) *Handler { return &Handler{join: j, opt: opt} }

// WithMe adds GET /queue/me to the handler.
func (h *Handler) WithMe(m MeGetter) *Handler { h.me = m; return h }

// WithServing adds GET /queue/serving to the handler.
func (h *Handler) WithServing(s ServingGetter) *Handler { h.serving = s; return h }

// RegisterRoutes mounts the queue routes.
func RegisterRoutes(mux *http.ServeMux, h *Handler) {
	mux.HandleFunc("POST /events/{event_id}/queue/join", h.Join)
	if h.me != nil {
		mux.HandleFunc("GET /events/{event_id}/queue/me", h.Me)
	}
	if h.serving != nil {
		mux.HandleFunc("GET /events/{event_id}/queue/serving", h.Serving)
	}
}

type joinBody struct {
	CaptchaToken string `json:"captcha_token"`
}

// Join handles one join request.
func (h *Handler) Join(w http.ResponseWriter, r *http.Request) {
	var body joinBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&body); err != nil {
		apinizer.WriteError(w, apperror.ErrBadRequest("Body tidak valid").WithCode(domain.CodeValidation))
		return
	}

	bearer, ok := grantauth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		apinizer.WriteError(w, apperror.ErrUnauthorized("Sesi tidak valid").WithCode(domain.CodeTokenInvalid))
		return
	}
	in := usecase.JoinInput{
		EventID:      r.PathValue("event_id"),
		CaptchaToken: body.CaptchaToken,
		DeviceID:     r.Header.Get("X-Device-Id"),
		Bearer:       bearer,
		QueueToken:   r.Header.Get(HeaderQueueToken),
		ClientIP:     ClientIP(r),
	}

	res, err := h.join.Join(r.Context(), in)
	if err != nil {
		apinizer.WriteError(w, err)
		return
	}
	switch res.Outcome {
	case usecase.Rejoined:
		apinizer.WriteEnvelope(w, http.StatusOK, "Rejoined existing queue position", rejoinedData{
			placeData: h.place(in.EventID, res),
			Rejoined:  true,
			State:     res.State,
		})
	default:
		apinizer.WriteEnvelope(w, http.StatusOK, "Successfully joined queue", queuedData{
			placeData: h.place(in.EventID, res),
			Event:     eventData{ID: in.EventID, Name: res.EventName},
		})
	}
}

type placeData struct {
	TicketNo   int64  `json:"ticket_no"`
	NowServing int64  `json:"now_serving"`
	Position   int64  `json:"position"`
	QueueToken string `json:"queue_token"`
	SSEURL     string `json:"sse_url"`
}

type eventData struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type queuedData struct {
	placeData
	Event eventData `json:"event"`
}

type rejoinedData struct {
	placeData
	Rejoined bool   `json:"rejoined"`
	State    string `json:"state"`
}

func (h *Handler) place(eventID string, res usecase.JoinResult) placeData {
	return placeData{
		TicketNo:   res.Engagement.TicketNo,
		NowServing: res.NowServing,
		Position:   res.Position(),
		QueueToken: res.Engagement.QueueToken,
		SSEURL:     h.SSEURL(eventID),
	}
}

// SSEURL is the stream for eventID (api-contracts §3.2).
func (h *Handler) SSEURL(eventID string) string {
	return strings.TrimRight(h.opt.SSEBaseURL, "/") + "/events/" + url.PathEscape(eventID) + "/queue/stream"
}

// ClientIP is the caller's address as API Gateway saw it. X-Forwarded-For is
// never read: the client controls it.
func ClientIP(r *http.Request) string {
	if gw, ok := core.GetAPIGatewayV2ContextFromContext(r.Context()); ok && gw.HTTP.SourceIP != "" {
		return gw.HTTP.SourceIP
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Me handles GET /events/{event_id}/queue/me: where is this customer now? It is a read, not cacheable.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	bearer, ok := grantauth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		apinizer.WriteError(w, apperror.ErrUnauthorized("Sesi tidak valid").WithCode(domain.CodeTokenInvalid))
		return
	}
	in := usecase.MeInput{
		EventID:    r.PathValue("event_id"),
		DeviceID:   r.Header.Get("X-Device-Id"),
		Bearer:     bearer,
		QueueToken: r.Header.Get(HeaderQueueToken),
	}
	w.Header().Set("Cache-Control", "no-store")
	res, err := h.me.Get(r.Context(), in)
	if err != nil {
		apinizer.WriteError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", meData(res))
}

// meData is the contract's `data` for each state (api-contracts §3.3): only the fields of that state.
func meData(res usecase.MeResult) map[string]any {
	d := map[string]any{"state": res.State}
	set := func(key string, t time.Time) { // a deadline that was never set is left out, never sent as year 1
		if !t.IsZero() {
			d[key] = t.UTC().Format(time.RFC3339)
		}
	}
	serverTime := res.ServerTime.UTC().Format("2006-01-02T15:04:05.000Z")
	switch res.State {
	case domain.MeQueued:
		d["ticket_no"], d["now_serving"] = res.TicketNo, res.NowServing
	case domain.MeGranted:
		d["grant_token"], d["server_time"] = res.GrantToken, serverTime
		set("booking_expires_at", res.BookingExpiresAt)
	case domain.MeSelecting:
		d["grant_token"], d["booking_id"], d["server_time"] = res.GrantToken, res.BookingID, serverTime
		set("booking_expires_at", res.BookingExpiresAt)
	case domain.MeHeld:
		d["grant_token"], d["booking_id"], d["server_time"] = res.GrantToken, res.BookingID, serverTime
		set("payment_expires_at", res.PaymentExpiresAt)
	case domain.MeAwaitingPayment:
		d["grant_token"], d["booking_id"] = res.GrantToken, res.BookingID
		d["payment_ref"], d["server_time"] = res.PaymentRef, serverTime
		set("payment_expires_at", res.PaymentExpiresAt)
	case domain.MeConfirmed:
		d["booking_id"] = res.BookingID
	}
	return d
}
