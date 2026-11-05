package usecase

import (
	"context"
	"log/slog"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/valueobject"
)

// Validate verifies a NIK, fail closed.
//
// The order of the steps is the design, not an implementation detail:
//
//  1. normalise and check format — a malformed NIK must never cost a paid call
//  2. stored evidence (registered, saved) — free, and the reason NIK_Terdaftar
//     and Daftar NIK Teman exist at all (NFR-12)
//  3. the 24-hour cache — a Dukcapil answer that is still current
//  4. circuit breaker — if Dukcapil is unusable, stop here and say so
//  5. rate limiter — the contracted RPS is a hard ceiling on the whole sale
//  6. Dukcapil
//
// Steps 2 and 3 are checked before 4 on purpose: a NIK with stored proof is
// answerable while Dukcapil is down, and refusing it would throw away the
// entire benefit of storing proof at exactly the moment it matters most.
func (u *NikUsecase) Validate(ctx context.Context, req dto.ValidateRequest) (dto.ValidateResponse, error) {
	normalized := valueobject.Normalize(req.NIK)
	if !valueobject.IsValidFormat(normalized) {
		return dto.ValidateResponse{}, apperror.
			ErrBadRequest("NIK must be exactly 16 digits").
			WithCode(apperror.CodeValidationError)
	}

	hash := valueobject.Hash(normalized, u.pepper)
	masked := valueobject.Mask(normalized)

	// 2. Stored evidence.
	source, err := u.evidence.FindEvidence(ctx, hash)
	if err != nil {
		// A database that cannot answer is not proof of absence. Falling
		// through to Dukcapil would be correct but costs money on every
		// request during an outage, so this fails closed like any other
		// dependency failure.
		slog.ErrorContext(ctx, "evidence lookup failed", slog.Any("err", err))
		return dto.ValidateResponse{}, apperror.
			ErrServiceUnavailable("verification evidence is unavailable").
			WithCode(apperror.CodeDukcapilUnavailable)
	}
	if source != "" {
		return respond(hash, masked, true, entity.StatusActive, source), nil
	}

	// 3. Cache: a live Dukcapil "valid" inside its 24-hour window.
	cached, err := u.cache.IsVerified(ctx, hash)
	if err != nil {
		// Unlike the evidence lookup, a cache miss-by-error is safe to treat as
		// a miss: the worst outcome is one extra Dukcapil call, and failing the
		// request instead would make Redis an availability dependency of every
		// sale.
		slog.WarnContext(ctx, "verification cache unavailable, falling through", slog.Any("err", err))
	}
	if cached {
		return respond(hash, masked, true, entity.StatusActive, entity.SourceVerified), nil
	}

	// 4. Breaker. OPEN means Dukcapil is unusable; admission is already paused
	// on the back of this same state (BR-13a), so there is nothing to retry.
	state, err := u.breaker.State(ctx)
	if err != nil {
		slog.WarnContext(ctx, "breaker state unavailable, assuming closed", slog.Any("err", err))
		state = entity.BreakerClosed
	}
	if state == entity.BreakerOpen {
		return dto.ValidateResponse{}, apperror.
			ErrServiceUnavailable("NIK verification is temporarily unavailable").
			WithCode(apperror.CodeDukcapilUnavailable)
	}

	// 5. Rate limit. Exceeding the contracted rate is indistinguishable, from
	// the customer's side, from Dukcapil being down — and it must be, because
	// the alternative is breaching the contract and getting throttled upstream.
	allowed, err := u.limiter.Allow(ctx)
	if err != nil {
		slog.WarnContext(ctx, "rate limiter unavailable, allowing call", slog.Any("err", err))
		allowed = true
	}
	if !allowed {
		slog.WarnContext(ctx, "dukcapil call throttled locally", slog.String("metric", "DukcapilThrottled"))
		return dto.ValidateResponse{}, apperror.
			ErrServiceUnavailable("NIK verification is temporarily unavailable").
			WithCode(apperror.CodeDukcapilUnavailable)
	}

	// 6. Dukcapil.
	result, err := u.dukcapil.VerifyNIK(ctx, normalized)
	if err != nil {
		if recErr := u.breaker.RecordFailure(ctx); recErr != nil {
			slog.ErrorContext(ctx, "failed to record breaker failure", slog.Any("err", recErr))
		}
		slog.ErrorContext(ctx, "dukcapil call failed",
			slog.Any("err", err), slog.String("metric", "DukcapilBlockedSales"))
		return dto.ValidateResponse{}, apperror.
			ErrServiceUnavailable("NIK verification is temporarily unavailable").
			WithCode(apperror.CodeDukcapilUnavailable)
	}

	// Dukcapil answered. That is a success for breaker purposes even when the
	// answer is "this NIK is not valid" — otherwise a run of customers
	// mistyping their NIKs would trip the breaker and pause the whole sale.
	if recErr := u.breaker.RecordSuccess(ctx); recErr != nil {
		slog.ErrorContext(ctx, "failed to record breaker success", slog.Any("err", recErr))
	}

	if !result.Valid {
		slog.InfoContext(ctx, "nik rejected by dukcapil",
			slog.String("status", result.Status), slog.String("metric", "NikRejectedRate"))
		return respond(hash, masked, false, entity.Status(result.Status), entity.SourceVerified), nil
	}

	// Cache only positive answers. Caching a rejection would strand a customer
	// who mistyped once behind a 24-hour window.
	if err := u.cache.MarkVerified(ctx, hash, entity.CacheTTL); err != nil {
		// The answer is already correct; losing the cache entry costs one more
		// Dukcapil call later and must not fail a verification that succeeded.
		slog.WarnContext(ctx, "failed to cache verification", slog.Any("err", err))
	}

	return respond(hash, masked, true, entity.StatusActive, entity.SourceVerified), nil
}

func respond(hash, masked string, valid bool, status entity.Status, source entity.Source) dto.ValidateResponse {
	return dto.ValidateResponse{
		NIKMasked: masked,
		NIKHmac:   hash,
		Valid:     valid,
		Status:    string(status),
		Source:    string(source),
	}
}
