package http

import (
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
)

// servingCacheControl lets a CDN keep one copy per event for 1 s: now_serving is at most about 1 s stale and the
// origin sees about 1 request per second per edge, whatever the crowd (api-contracts §3.4).
const servingCacheControl = "public, max-age=1"

// servingData is the contract's data for GET /queue/serving, in Apidog field order.
type servingData struct {
	NowServing  int64      `json:"now_serving"`
	RatePerMin  int64      `json:"rate_per_min"`
	State       string     `json:"state"`
	ServerTime  string     `json:"server_time"`
	PollAfterMS int64      `json:"poll_after_ms"`
	PollTiers   []pollTier `json:"poll_tiers"`
}

type pollTier struct {
	MaxPosition *int64 `json:"max_position"` // null = no limit
	PollAfterMS int64  `json:"poll_after_ms"`
}

// Serving handles GET /events/{event_id}/queue/serving, the polling fallback of the SSE stream. It reads nothing
// from the caller (no auth, cookies, headers or query string) and writes no cookie, so a CDN can share one answer.
// Only a 200 is cacheable: an error must not be served to the whole crowd from the edge.
func (h *Handler) Serving(w http.ResponseWriter, r *http.Request) {
	res, err := h.serving.Get(r.Context(), r.PathValue("event_id"))
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		apinizer.WriteError(w, err)
		return
	}
	w.Header().Set("Cache-Control", servingCacheControl)
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", newServingData(res))
}

func newServingData(res usecase.ServingResult) servingData {
	tiers := make([]pollTier, len(res.PollTiers))
	for i, t := range res.PollTiers {
		tiers[i].PollAfterMS = t.PollAfter.Milliseconds()
		if t.MaxPosition > 0 {
			maxPos := t.MaxPosition
			tiers[i].MaxPosition = &maxPos
		}
	}
	return servingData{
		NowServing:  res.NowServing,
		RatePerMin:  res.RatePerMin,
		State:       string(res.State),
		ServerTime:  res.ServerTime.UTC().Format("2006-01-02T15:04:05.000Z"),
		PollAfterMS: res.PollAfter.Milliseconds(),
		PollTiers:   tiers,
	}
}
