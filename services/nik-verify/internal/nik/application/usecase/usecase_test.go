package usecase

import (
	"testing"

	"go.uber.org/mock/gomock"

	mock_outbound "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/_mock/nik/port/outbound"
)

const testPepper = "test-pepper"

type deps struct {
	dukcapil *mock_outbound.MockIDukcapilAdapter
	evidence *mock_outbound.MockIEvidenceRepository
	cache    *mock_outbound.MockIVerificationCache
	limiter  *mock_outbound.MockIRateLimiter
	breaker  *mock_outbound.MockICircuitBreaker
}

// newTestUsecase wires a NikUsecase backed by mocks.
func newTestUsecase(t *testing.T) (*NikUsecase, deps) {
	t.Helper()
	ctrl := gomock.NewController(t)
	d := deps{
		dukcapil: mock_outbound.NewMockIDukcapilAdapter(ctrl),
		evidence: mock_outbound.NewMockIEvidenceRepository(ctrl),
		cache:    mock_outbound.NewMockIVerificationCache(ctrl),
		limiter:  mock_outbound.NewMockIRateLimiter(ctrl),
		breaker:  mock_outbound.NewMockICircuitBreaker(ctrl),
	}
	uc := NewNik(d.dukcapil, d.evidence, d.cache, d.limiter, d.breaker, testPepper).(*NikUsecase)
	return uc, d
}
