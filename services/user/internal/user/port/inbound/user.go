package inbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// IUserUsecase is the inbound port for all user-domain business logic.
// HTTP handlers depend only on this interface, never on concrete use cases.
type IUserUsecase interface {
	// Auth
	RequestMagicLink(ctx context.Context, req dto.RequestMagicLinkRequest) error
	VerifyMagicLink(ctx context.Context, token string) (dto.VerifyMagicLinkResponse, error)
	Register(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error)
	VerifyEmail(ctx context.Context, token string) error
	// RefreshSession redeems a refresh token and issues a new pair.
	RefreshSession(ctx context.Context, refreshToken string) (dto.RefreshSessionResponse, error)

	// ValidateAccessToken resolves a bearer token to its account ID.
	ValidateAccessToken(ctx context.Context, token string) (accountID string, err error)

	// Saved NIKs (FR-32a..d)
	GetSavedNIKs(ctx context.Context, accountID string) (dto.SavedNIKsResponse, error)
	AddSavedNIK(ctx context.Context, accountID string, req dto.AddSavedNIKRequest) (dto.SavedNIKItem, error)
	DeleteSavedNIK(ctx context.Context, accountID, nikID string) error

	// Tickets (contract §6)
	GetMyTickets(ctx context.Context, accountID string, req dto.GetMyTicketsRequest) (dto.MyTicketsResponse, error)
	GetETicket(ctx context.Context, accountID, ticketID string) (dto.ETicketResponse, error)
}
