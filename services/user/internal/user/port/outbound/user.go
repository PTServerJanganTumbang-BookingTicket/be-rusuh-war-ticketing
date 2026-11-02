package outbound

import (
	"context"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

// IUserRepository defines the outbound port (driven port) for user-domain persistence.
// All adapters (in-memory, Postgres, …) must satisfy this interface.
type IUserRepository interface {
	// Account
	FindAccountByEmail(ctx context.Context, email string) (entity.Account, error)
	FindAccountByID(ctx context.Context, id string) (entity.Account, error)
	SaveAccount(ctx context.Context, account entity.Account) (entity.Account, error)
	UpdateAccount(ctx context.Context, account entity.Account) (entity.Account, error)
	IsNIKRegistered(ctx context.Context, nikHmac string) (bool, error)

	// Consent records (append-only, UU PDP)
	SaveConsentRecord(ctx context.Context, record entity.ConsentRecord) error

	// Magic link tokens
	SaveMagicToken(ctx context.Context, token entity.MagicToken) error
	FindMagicTokenByValue(ctx context.Context, token string) (entity.MagicToken, error)
	MarkMagicTokenUsed(ctx context.Context, tokenID string) error

	// Access tokens (bearer tokens issued after magic-link verification)
	SaveAccessToken(ctx context.Context, token entity.AccessToken) error
	FindAccessTokenByValue(ctx context.Context, token string) (entity.AccessToken, error)

	// Saved NIKs
	CountSavedNIKsByAccountID(ctx context.Context, accountID string) (int, error)
	IsNIKAlreadySaved(ctx context.Context, accountID, nikHmac string) (bool, error)
	SaveSavedNIK(ctx context.Context, nik entity.SavedNIK) (entity.SavedNIK, error)
	FindSavedNIKsByAccountID(ctx context.Context, accountID string) ([]entity.SavedNIK, error)
	FindSavedNIKByID(ctx context.Context, accountID, id string) (entity.SavedNIK, error)
	DeleteSavedNIK(ctx context.Context, accountID, id string) error

	// Tickets
	FindTicketsByAccountID(ctx context.Context, accountID string, req dto.GetMyTicketsRequest) ([]entity.UserTicket, int, error)
	FindTicketByID(ctx context.Context, accountID, ticketID string) (entity.UserTicket, error)
}
