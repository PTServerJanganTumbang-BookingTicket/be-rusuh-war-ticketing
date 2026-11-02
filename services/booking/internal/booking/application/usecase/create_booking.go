package usecase

import (
	"context"
	"fmt"
	"time"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/entity"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/mapper"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/valueobject"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/idgen"
)

// CreateBooking holds the requested seats and opens the payment windo
func (u *BookingUsecase) CreateBooking(ctx context.Context, req dto.CreateBookingRequest) (dto.BookingResponse, error) {
	if len(req.Tickets) > 3 {
		return dto.BookingResponse{}, apperror.ErrBadRequest("Maximum 3 tickets per order")
	}

	now := time.Now().UTC()
	tickets := make([]entity.Ticket, 0, len(req.Tickets))
	var subtotal int64
	for _, t := range req.Tickets {
		cat, ok := valueobject.Lookup(t.CategoryID)
		if !ok {
			return dto.BookingResponse{}, apperror.ErrBadRequest(fmt.Sprintf("unknown category %q", t.CategoryID))
		}
		tickets = append(tickets, entity.Ticket{
			CategoryID:   cat.ID,
			CategoryName: cat.Name,
			Price:        cat.Price,
			NIK:          t.NIK,
			HolderEmail:  t.HolderEmail,
		})
		subtotal += cat.Price
	}

	booking := entity.Booking{
		ID: idgen.NewPrefixed(entity.BookingIDPrefix),
		Event: entity.Event{
			ID:   req.EventID,
			Name: req.EventID,
		},
		Tickets:    tickets,
		Subtotal:   subtotal,
		ServiceFee: 0,
		Total:      subtotal,
		Status:     entity.StatusPendingPayment,
		CreatedAt:  now,
		ExpiresAt:  now.Add(entity.PaymentTTLSeconds * time.Second),
	}

	saved, err := u.repo.Save(ctx, booking)
	if err != nil {
		return dto.BookingResponse{}, err
	}

	return mapper.ToBookingResponse(saved, now), nil
}
