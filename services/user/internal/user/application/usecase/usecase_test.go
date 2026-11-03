package usecase

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	mock_outbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/_mock/user/port/outbound"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

const testAccountID = "USR-001"

// accessTokenTestTTL mirrors config's ACCESS_TOKEN_TTL_SECONDS default, which
// is derived from SALE_WINDOW_SECONDS rather than from the journey (ADR-005 §7):
// 21600 + 990 + 150.
const accessTokenTestTTL = 22740 * time.Second

type deps struct {
	repo       *mock_outbound.MockIUserRepository
	engagement *mock_outbound.MockIEngagementRepository
	verifier   *mock_outbound.MockINikVerifier
	limiter    *mock_outbound.MockIRateLimiter
	tokens     *token.Issuer
}

// newTestUsecase wires a UserUsecase backed by mocks.
func newTestUsecase(t *testing.T) (*UserUsecase, deps) {
	t.Helper()
	ctrl := gomock.NewController(t)
	// The production TTL (22740s), so the ExpiresIn assertions below check the
	// figure the contract actually states rather than a test-only number.
	issuer, err := token.NewIssuer("test-signing-secret", "rusuh-user", accessTokenTestTTL)
	require.NoError(t, err)

	d := deps{
		repo:       mock_outbound.NewMockIUserRepository(ctrl),
		engagement: mock_outbound.NewMockIEngagementRepository(ctrl),
		verifier:   mock_outbound.NewMockINikVerifier(ctrl),
		limiter:    mock_outbound.NewMockIRateLimiter(ctrl),
		tokens:     issuer,
	}
	uc := NewUser(d.repo, d.engagement, d.verifier, d.limiter, d.tokens).(*UserUsecase)
	return uc, d
}

// allowRateLimit sets the common expectation that the limiter permits a call.
func (d deps) allowRateLimit() {
	d.limiter.EXPECT().Allow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(true, time.Duration(0), nil)
}

// noEngagement is the ordinary case: the account has nothing in flight.
func (d deps) noEngagement() {
	d.engagement.EXPECT().FindActiveEngagement(gomock.Any(), gomock.Any()).
		Return(entity.Engagement{Kind: entity.EngagementNone}, nil)
}

// issuesSession expects the refresh token a successful login opens.
func (d deps) issuesSession() {
	d.repo.EXPECT().CreateRefreshToken(gomock.Any(), gomock.Any()).Return(nil)
}
