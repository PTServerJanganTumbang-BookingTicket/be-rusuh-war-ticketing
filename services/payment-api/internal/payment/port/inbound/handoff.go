package inbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
)

// IHandoffUsecase is the inbound port of the WebView payment handoff (ADR-007, api-contracts §5.6, §5.7).
type IHandoffUsecase interface {
	// CreateHandoff is §5.6: a one-time code for the booking the grant holds.
	CreateHandoff(ctx context.Context, bookingID, grantToken string) (dto.HandoffResponse, error)
	// ExchangeHandoff is §5.7: the code traded for a payment session.
	ExchangeHandoff(ctx context.Context, req dto.ExchangeRequest) (dto.ExchangeResponse, error)
}
