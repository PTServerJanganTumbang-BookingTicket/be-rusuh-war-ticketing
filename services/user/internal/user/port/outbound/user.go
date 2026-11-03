package outbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

// Registration is the complete set of writes one registration makes.
//
// All four commit together or none do, because every partial outcome is a
// defect that nothing downstream can detect:
//
//   - account without Consent — an account holding a Dukcapil-verified NIK
//     with no record of consent is a UU PDP violation sitting in the database
//     (FR-31d, NFR-11)
//   - account without Event — an account that never receives its verification
//     email, with nothing to retry
//   - account without VerifyToken — an emailed link that cannot be redeemed
//   - Event without account — an email about an account that rolled back
type Registration struct {
	Account     entity.Account
	Consent     entity.ConsentRecord
	VerifyToken entity.MagicToken
	Event       entity.OutboxEvent
}

// IUserRepository is the outbound port for user-domain persistence.
//
// Several methods take an OutboxEvent alongside the state they write. That is
// not a convenience: the event and the state change must commit together, or a
// crash in between leaves an account with no verification email queued, or an
// email for an account that rolled back (ADR-005 §9). Keeping both in one
// method keeps the transaction inside the adapter, so no port has to hand a
// transaction handle to the use case.
type IUserRepository interface {
	// Account
	FindAccountByEmail(ctx context.Context, email string) (entity.Account, error)
	FindAccountByID(ctx context.Context, id string) (entity.Account, error)
	MarkEmailVerified(ctx context.Context, accountID string) error

	// RegisterAccount inserts everything a registration consists of in ONE
	// transaction. See Registration for why all four belong together.
	RegisterAccount(ctx context.Context, reg Registration) (entity.Account, error)

	// HasConsent reports whether the account has ever recorded consent.
	// Saving someone else's NIK requires it (contract §1.6).
	HasConsent(ctx context.Context, accountID string) (bool, error)

	// Magic links
	// SaveMagicTokenWithEvent stores the token and queues its email in ONE
	// transaction. A token with no queued email is a link nobody receives; a
	// queued email with no token is a link that cannot be redeemed.
	SaveMagicTokenWithEvent(ctx context.Context, token entity.MagicToken, event entity.OutboxEvent) error
	// ConsumeMagicToken atomically marks an unused, unexpired token of the
	// given purpose as used and returns it. Atomic because two clicks on one
	// emailed link race, and a check-then-update would let both win.
	ConsumeMagicToken(ctx context.Context, tokenHash string, purpose entity.Purpose) (entity.MagicToken, error)

	// Refresh tokens
	CreateRefreshToken(ctx context.Context, token entity.RefreshToken) error
	// RotateRefreshToken redeems a token and issues its replacement in ONE
	// transaction.
	//
	// It must return ErrRefreshTokenReplayed when the presented token was
	// already redeemed, and revoke that token's whole family before returning:
	// a replay means the credential leaked, and the holder of the newest link
	// in the chain may be the attacker (OAuth 2.0 BCP §4.13).
	RotateRefreshToken(ctx context.Context, presentedHash string, replacement entity.RefreshToken) (entity.RefreshToken, error)
	// RevokeRefreshFamily invalidates every live token in a rotation chain.
	RevokeRefreshFamily(ctx context.Context, familyID string) error

	// Saved NIKs
	// CreateSavedNIKWithinCap inserts an entry while holding a row lock on the
	// owning account, so concurrent adds cannot both pass the BR-12 cap.
	CreateSavedNIKWithinCap(ctx context.Context, nik entity.SavedNIK, maxEntries int) (entity.SavedNIK, error)
	FindSavedNIKsByAccountID(ctx context.Context, accountID string) ([]entity.SavedNIK, error)
	DeleteSavedNIK(ctx context.Context, accountID, id string) error

	// Tickets
	FindTicketsByAccountID(ctx context.Context, accountID string, req dto.GetMyTicketsRequest) ([]entity.UserTicket, int, error)
	FindTicketByID(ctx context.Context, accountID, ticketID string) (entity.UserTicket, error)
}
