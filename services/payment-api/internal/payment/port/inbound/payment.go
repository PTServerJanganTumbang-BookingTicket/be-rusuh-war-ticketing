package inbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
)

// IPaymentUsecase is the inbound port for the five payment endpoints.
type IPaymentUsecase interface {
	// GetMethods is §5.1.
	GetMethods(ctx context.Context, bookingID, grantToken string) (dto.MethodsResponse, error)
	// Initiate is §5.2.
	Initiate(ctx context.Context, bookingID, grantToken string, req dto.InitiateRequest) (dto.InitiateResponse, error)
	// Retry is §5.3 — cancel and switch method.
	Retry(ctx context.Context, bookingID, grantToken string) (dto.RetryResponse, error)
	// GetStatus is §5.4.
	GetStatus(ctx context.Context, bookingID, grantToken string) (dto.StatusResponse, error)
	// HandleNotification is §5.5. It returns an error only for conditions the
	// caller should log; the HTTP layer answers 200 regardless.
	HandleNotification(ctx context.Context, n dto.Notification) error
}
