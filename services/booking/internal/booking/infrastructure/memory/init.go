package memory

import (
	"context"
	"sync"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// BookingRepository is an in-memory implementation of IBookingRepository.
type BookingRepository struct {
	mu    sync.RWMutex
	store map[string]entity.Booking
}

// NewBookingRepository returns the outbound repository port backed by an in-memory map.
func NewBookingRepository() portout.IBookingRepository {
	return &BookingRepository{
		store: make(map[string]entity.Booking),
	}
}

func (r *BookingRepository) Save(_ context.Context, booking entity.Booking) (entity.Booking, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[booking.ID] = booking
	return booking, nil
}

func (r *BookingRepository) FindByID(_ context.Context, id string) (entity.Booking, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	booking, ok := r.store[id]
	if !ok {
		return entity.Booking{}, apperror.ErrNotFound("booking not found")
	}
	return booking, nil
}
