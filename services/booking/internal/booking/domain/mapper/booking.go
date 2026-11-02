package mapper

import (
	"time"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/entity"
)

// maskNIK renders a 16-digit NIK as "3171********xx22"
func maskNIK(nik string) string {
	if len(nik) != 16 {
		return "****************"
	}
	return nik[:4] + "********xx" + nik[14:]
}

// ToBookingResponse maps a Booking entity to its contract response DTO
func ToBookingResponse(b entity.Booking, serverTime time.Time) dto.BookingResponse {
	tickets := make([]dto.TicketResponse, 0, len(b.Tickets))
	for _, t := range b.Tickets {
		tickets = append(tickets, dto.TicketResponse{
			Category:  t.CategoryName,
			Price:     t.Price,
			NIKMasked: maskNIK(t.NIK),
		})
	}

	return dto.BookingResponse{
		BookingID: b.ID,
		Status:    string(b.Status),
		Event: dto.EventSummary{
			ID:    b.Event.ID,
			Name:  b.Event.Name,
			Date:  b.Event.Date,
			Venue: b.Event.Venue,
		},
		Tickets: tickets,
		Summary: dto.Summary{
			Subtotal:   b.Subtotal,
			ServiceFee: b.ServiceFee,
			Total:      b.Total,
		},
		PaymentTTL: dto.PaymentTTL{
			ExpiresAt:  b.ExpiresAt.UTC().Format(time.RFC3339),
			TTLSeconds: int(b.ExpiresAt.Sub(serverTime).Seconds()),
			ServerTime: serverTime.UTC().Format(time.RFC3339),
		},
		RedirectURL: "/bookings/" + b.ID + "/payment",
	}
}
