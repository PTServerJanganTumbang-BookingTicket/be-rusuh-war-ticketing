package usecase

import (
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/inbound"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/outbound"
)

// NikUsecase implements NIK verification with the guards ADR-005 §8 requires.
type NikUsecase struct {
	dukcapil portout.IDukcapilAdapter
	evidence portout.IEvidenceRepository
	cache    portout.IVerificationCache
	limiter  portout.IRateLimiter
	breaker  portout.ICircuitBreaker

	// pepper is the KMS-managed HMAC secret. This service is the only one that
	// holds it; every other service receives hashes.
	pepper string
}

// NewNik wires the use case and returns the inbound port.
func NewNik(
	dukcapil portout.IDukcapilAdapter,
	evidence portout.IEvidenceRepository,
	cache portout.IVerificationCache,
	limiter portout.IRateLimiter,
	breaker portout.ICircuitBreaker,
	pepper string,
) portin.INikUsecase {
	return &NikUsecase{
		dukcapil: dukcapil,
		evidence: evidence,
		cache:    cache,
		limiter:  limiter,
		breaker:  breaker,
		pepper:   pepper,
	}
}
