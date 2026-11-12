package usecase

import (
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/inbound"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// Rate limits from the API contract (§1.1) and §9 "per user on auth".
const (
	MagicLinkLimit  = 5
	MagicLinkWindow = time.Hour
)

// UserUsecase implements the inbound port for all user-domain business logic.
//
// Note what is NOT here: no HMAC key — hashing happens in nik-verify, which
// returns the hash, so the NIK pepper never enters this process (ADR-005 §8).
// And no mailer: email is queued through the transactional outbox, so this
// service never talks to SES and an email provider outage cannot fail a
// registration (NFR-10).
type UserUsecase struct {
	repo         portout.IUserRepository
	engagement   portout.IEngagementRepository
	verifier     portout.INikVerifier
	limiter      portout.IRateLimiter
	tokens       *token.Issuer
	maxSavedNIKs int
}

// NewUser wires the use case and returns the inbound port.
func NewUser(
	repo portout.IUserRepository,
	engagement portout.IEngagementRepository,
	verifier portout.INikVerifier,
	limiter portout.IRateLimiter,
	tokens *token.Issuer,
	maxSavedNIKs int,
) portin.IUserUsecase {
	return &UserUsecase{
		repo:         repo,
		engagement:   engagement,
		verifier:     verifier,
		limiter:      limiter,
		tokens:       tokens,
		maxSavedNIKs: maxSavedNIKs,
	}
}
