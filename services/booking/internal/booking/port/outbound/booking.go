package outbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain/entity"
)

// IBookingRepository defines the outbound port (driven port) for booking persistence.
type IBookingRepository interface {
	Save(ctx context.Context, booking entity.Booking) (entity.Booking, error)
	FindByID(ctx context.Context, id string) (entity.Booking, error)
}
