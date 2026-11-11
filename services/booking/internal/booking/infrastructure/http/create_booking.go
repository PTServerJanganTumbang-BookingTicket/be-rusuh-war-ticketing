package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
)

// maxBodyBytes bounds the body of POST /bookings: at most three seats and an email, far below this.
const maxBodyBytes = 64 << 10

// CreateBooking handles POST /bookings. It must sit behind pkg/grantauth.Middleware.
type CreateBooking struct{ uc *usecase.CreateBooking }

// NewCreateBooking builds the handler.
func NewCreateBooking(uc *usecase.CreateBooking) *CreateBooking { return &CreateBooking{uc: uc} }

// ServeHTTP answers 201 for a booking this call created and 200 for one that was already held. The body is parsed lazily,
// only once the status allows it: a retry answers 200 whatever the body says, even one that is not JSON.
// Unknown fields (holder_email, terms_version from an old client) are ignored.
func (h *CreateBooking) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g, ok := grantauth.FromContext(r.Context())
	if !ok {
		apinizer.WriteError(w, grantauth.ErrMissing())
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	res, err := h.uc.CreateFrom(r.Context(), g, func() (usecase.CreateRequest, error) {
		var req usecase.CreateRequest
		dec := json.NewDecoder(body)
		if err := dec.Decode(&req); err != nil {
			return usecase.CreateRequest{}, errors.New("unreadable body")
		}
		// One JSON value and nothing after it: trailing data is a malformed body, not something to process.
		if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return usecase.CreateRequest{}, errors.New("unreadable body")
		}
		return req, nil
	})
	if err != nil {
		apinizer.WriteError(w, err)
		return
	}
	if res.Created {
		apinizer.WriteEnvelope(w, http.StatusCreated, "Booking created", res.View)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", res.View)
}
