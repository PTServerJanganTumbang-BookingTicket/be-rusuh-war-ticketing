// Package http holds the two public handlers. Both set Cache-Control themselves: the CDN caches them (api-contracts §2).
package http

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
)

// Caching is how long a CDN may keep a normal answer, and a degraded one (Redis down).
type Caching struct{ MaxAge, DegradedMaxAge int }

func (c Caching) set(w http.ResponseWriter, degraded bool) {
	age := c.MaxAge
	if degraded {
		age = c.DegradedMaxAge
	}
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", age))
}

// writeError answers an error: 4xx may be cached (a bot hammering a bad id must hit the CDN, not Lambda), 5xx never.
func (c Caching) writeError(w http.ResponseWriter, err error) {
	var ae *apperror.AppError
	if errors.As(err, &ae) && ae.HTTPStatus < 500 {
		c.set(w, false)
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	apinizer.WriteError(w, err)
}

// ---- GET /events

const (
	defaultLimit = 10
	maxLimit     = 50
)

type location struct {
	Venue string `json:"venue"`
	City  string `json:"city"`
}

type display struct {
	Enabled bool `json:"enabled"`
}

type listItem struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Category     string     `json:"category"`
	Location     location   `json:"location"`
	Date         string     `json:"date"`
	ImageURL     string     `json:"image_url"`
	QueueState   string     `json:"queue_state"`
	SaleStartsAt *time.Time `json:"sale_starts_at"`
	Display      display    `json:"display"`
}

// ListEvents handles GET /events.
type ListEvents struct {
	uc *usecase.List
	c  Caching
}

// NewListEvents builds the handler.
func NewListEvents(uc *usecase.List, c Caching) *ListEvents { return &ListEvents{uc: uc, c: c} }

// ServeHTTP validates the query, runs the use case and writes the page.
func (h *ListEvents) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	in, err := parseList(r)
	if err != nil {
		h.c.writeError(w, err)
		return
	}
	res, err := h.uc.Do(r.Context(), in)
	if err != nil {
		h.c.writeError(w, err)
		return
	}
	items := make([]listItem, 0, len(res.Cards))
	for _, c := range res.Cards {
		items = append(items, listItem{
			ID: c.Event.ID, Name: c.Event.Name, Category: c.Event.Category,
			Location: location{Venue: c.Event.Venue, City: c.Event.City},
			Date:     c.Event.Date.Format("2006-01-02"), ImageURL: c.Event.ImageURL,
			QueueState: string(c.QueueState), SaleStartsAt: c.Event.SaleStartsAt,
			Display: display{Enabled: c.QueueState.Enabled()},
		})
	}
	h.c.set(w, res.Degraded)
	apinizer.WriteList(w, http.StatusOK, "Success", items, apinizer.NewPagination(res.Total, in.Limit, in.Offset))
}

func parseList(r *http.Request) (usecase.ListInput, error) {
	q := r.URL.Query()
	in := usecase.ListInput{Limit: defaultLimit}
	bad := func(field string) error {
		return apperror.ErrBadRequest("Parameter " + field + " tidak valid").WithCode(domain.CodeValidation)
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			return in, bad("limit")
		}
		in.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return in, bad("offset")
		}
		in.Offset = n
	}
	return in, nil
}

// ---- GET /events/{event_id}

type categoryItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int64  `json:"price"`
	Available   int64  `json:"available"`
	Status      string `json:"status"`
}

type captcha struct {
	Provider string `json:"provider"`
	SiteKey  string `json:"site_key"`
}

type detailItem struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Category     string         `json:"category"`
	Location     location       `json:"location"`
	Date         string         `json:"date"`
	Time         string         `json:"time"`
	ImageURL     string         `json:"image_url"`
	Description  string         `json:"description"`
	QueueState   string         `json:"queue_state"`
	SaleStartsAt *time.Time     `json:"sale_starts_at"`
	Display      display        `json:"display"`
	Categories   []categoryItem `json:"ticket_categories"`
	Captcha      captcha        `json:"captcha"`
}

// GetEvent handles GET /events/{event_id}.
type GetEvent struct {
	uc *usecase.Get
	c  Caching
}

// NewGetEvent builds the handler.
func NewGetEvent(uc *usecase.Get, c Caching) *GetEvent { return &GetEvent{uc: uc, c: c} }

// ServeHTTP runs the use case and writes the event.
func (h *GetEvent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d, err := h.uc.Do(r.Context(), r.PathValue("event_id"))
	if err != nil {
		h.c.writeError(w, err)
		return
	}
	cats := make([]categoryItem, 0, len(d.Categories))
	for _, c := range d.Categories {
		cats = append(cats, categoryItem{ID: c.ID, Name: c.Name, Description: c.Description, Price: c.Price, Available: c.Available, Status: c.Status})
	}
	e := d.Event
	h.c.set(w, d.Degraded)
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", detailItem{
		ID: e.ID, Name: e.Name, Category: e.Category, Location: location{Venue: e.Venue, City: e.City},
		Date: e.Date.Format("2006-01-02"), Time: e.Time, ImageURL: e.ImageURL, Description: e.Description,
		QueueState: string(d.QueueState), SaleStartsAt: e.SaleStartsAt, Display: display{Enabled: d.QueueState.Enabled()},
		Categories: cats, Captcha: captcha{Provider: "turnstile", SiteKey: d.CaptchaSiteKey},
	})
}
