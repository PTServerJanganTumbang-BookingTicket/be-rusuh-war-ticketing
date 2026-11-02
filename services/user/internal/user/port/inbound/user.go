package inbound

import (
	"context"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// IUserUsecase defines the inbound port (driving port) for all user-domain business logic.
// HTTP handlers depend only on this interface — never on concrete use case structs.
type IUserUsecase interface {
	// Auth
	RequestMagicLink(ctx context.Context, req dto.RequestMagicLinkRequest) error
	VerifyMagicLink(ctx context.Context, token string) (dto.VerifyMagicLinkResponse, error)
	Register(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error)

	// Token management (called by the HTTP layer to authenticate protected requests)
	ValidateAccessToken(ctx context.Context, token string) (accountID string, err error)

	// Saved NIKs (FR-15a)
	GetSavedNIKs(ctx context.Context, accountID string) (dto.SavedNIKsResponse, error)
	AddSavedNIK(ctx context.Context, accountID string, req dto.AddSavedNIKRequest) (dto.SavedNIKItem, error)
	DeleteSavedNIK(ctx context.Context, accountID, nikID string) error

	// Tickets (Section 6)
	GetMyTickets(ctx context.Context, accountID string, req dto.GetMyTicketsRequest) (dto.MyTicketsResponse, error)
	GetETicket(ctx context.Context, accountID, ticketID string) (dto.ETicketResponse, error)
}
