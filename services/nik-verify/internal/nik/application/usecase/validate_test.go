package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/valueobject"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/outbound"
)

const testNIK = "3171000000000022"

func req() dto.ValidateRequest { return dto.ValidateRequest{NIK: testNIK} }

// ---- format gate ----

func TestValidate_RejectsMalformedNIK_WithoutCallingAnything(t *testing.T) {
	// No EXPECT() calls are registered: a malformed NIK must not reach the
	// database, the cache or the paid API. gomock fails the test if it does.
	uc, _ := newTestUsecase(t)

	_, err := uc.Validate(context.Background(), dto.ValidateRequest{NIK: "123"})
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 400, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeValidationError, appErr.ErrorCode)
}

func TestValidate_NormalisesBeforeHashing(t *testing.T) {
	uc, d := newTestUsecase(t)
	wantHash := valueobject.Hash(testNIK, testPepper)

	d.evidence.EXPECT().FindEvidence(gomock.Any(), wantHash).Return(entity.SourceRegistered, nil)

	resp, err := uc.Validate(context.Background(), dto.ValidateRequest{NIK: "3171 0000 0000 0022"})
	require.NoError(t, err)
	assert.Equal(t, wantHash, resp.NIKHmac)
}

// ---- stored evidence short-circuits ----

func TestValidate_RegisteredNIK_SkipsDukcapil(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.SourceRegistered, nil)

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, string(entity.SourceRegistered), resp.Source)
	assert.Equal(t, "3171**********22", resp.NIKMasked)
}

func TestValidate_SavedNIK_SkipsDukcapil(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.SourceSaved, nil)

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, string(entity.SourceSaved), resp.Source)
}

func TestValidate_StoredEvidenceAnswersWhileBreakerIsOpen(t *testing.T) {
	// The point of storing evidence: a NIK already proven stays usable during a
	// Dukcapil outage. The breaker is never consulted, so no EXPECT for it.
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.SourceSaved, nil)

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
	assert.True(t, resp.Valid)
}

func TestValidate_EvidenceLookupFailure_FailsClosed(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), errors.New("db down"))

	_, err := uc.Validate(context.Background(), req())
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 503, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeDukcapilUnavailable, appErr.ErrorCode)
}

// ---- cache ----

func TestValidate_CacheHit_SkipsDukcapil(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), gomock.Any()).Return(true, nil)

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, string(entity.SourceVerified), resp.Source)
}

func TestValidate_CacheError_FallsThroughRatherThanFailing(t *testing.T) {
	// Redis must not become an availability dependency of every sale: a cache
	// that cannot answer costs one extra Dukcapil call, nothing more.
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), gomock.Any()).Return(false, errors.New("redis down"))
	d.breaker.EXPECT().State(gomock.Any()).Return(entity.BreakerClosed, nil)
	d.limiter.EXPECT().Allow(gomock.Any()).Return(true, nil)
	d.dukcapil.EXPECT().VerifyNIK(gomock.Any(), testNIK).Return(portout.DukcapilResult{Valid: true, Status: "ACTIVE"}, nil)
	d.breaker.EXPECT().RecordSuccess(gomock.Any()).Return(nil)
	d.cache.EXPECT().MarkVerified(gomock.Any(), gomock.Any(), entity.CacheTTL).Return(nil)

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
	assert.True(t, resp.Valid)
}

// ---- breaker and limiter ----

func TestValidate_BreakerOpen_FailsClosed(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), gomock.Any()).Return(false, nil)
	d.breaker.EXPECT().State(gomock.Any()).Return(entity.BreakerOpen, nil)

	_, err := uc.Validate(context.Background(), req())
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 503, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeDukcapilUnavailable, appErr.ErrorCode)
}

func TestValidate_Throttled_FailsClosed(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), gomock.Any()).Return(false, nil)
	d.breaker.EXPECT().State(gomock.Any()).Return(entity.BreakerClosed, nil)
	d.limiter.EXPECT().Allow(gomock.Any()).Return(false, nil)

	_, err := uc.Validate(context.Background(), req())
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 503, appErr.HTTPStatus)
}

// ---- Dukcapil ----

func TestValidate_DukcapilUnreachable_RecordsFailureAndFailsClosed(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), gomock.Any()).Return(false, nil)
	d.breaker.EXPECT().State(gomock.Any()).Return(entity.BreakerClosed, nil)
	d.limiter.EXPECT().Allow(gomock.Any()).Return(true, nil)
	d.dukcapil.EXPECT().VerifyNIK(gomock.Any(), testNIK).Return(portout.DukcapilResult{}, errors.New("timeout"))
	d.breaker.EXPECT().RecordFailure(gomock.Any()).Return(nil)

	_, err := uc.Validate(context.Background(), req())
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 503, appErr.HTTPStatus)
	assert.Equal(t, apperror.CodeDukcapilUnavailable, appErr.ErrorCode)
}

func TestValidate_RejectedNIK_Is200AndCountsAsBreakerSuccess(t *testing.T) {
	// A rejected NIK means Dukcapil is WORKING. Counting it as a failure would
	// let a run of customers mistyping their NIKs pause the entire sale.
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), gomock.Any()).Return(false, nil)
	d.breaker.EXPECT().State(gomock.Any()).Return(entity.BreakerClosed, nil)
	d.limiter.EXPECT().Allow(gomock.Any()).Return(true, nil)
	d.dukcapil.EXPECT().VerifyNIK(gomock.Any(), testNIK).Return(portout.DukcapilResult{Valid: false, Status: "NOT_FOUND"}, nil)
	d.breaker.EXPECT().RecordSuccess(gomock.Any()).Return(nil)

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
	assert.False(t, resp.Valid)
	assert.Equal(t, "NOT_FOUND", resp.Status)
}

func TestValidate_RejectedNIK_IsNotCached(t *testing.T) {
	// No MarkVerified expectation: caching a rejection would strand a customer
	// who mistyped once behind a 24-hour window.
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), gomock.Any()).Return(false, nil)
	d.breaker.EXPECT().State(gomock.Any()).Return(entity.BreakerClosed, nil)
	d.limiter.EXPECT().Allow(gomock.Any()).Return(true, nil)
	d.dukcapil.EXPECT().VerifyNIK(gomock.Any(), testNIK).Return(portout.DukcapilResult{Valid: false, Status: "INACTIVE"}, nil)
	d.breaker.EXPECT().RecordSuccess(gomock.Any()).Return(nil)

	_, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
}

func TestValidate_ValidNIK_CachesAndReturnsHash(t *testing.T) {
	uc, d := newTestUsecase(t)
	wantHash := valueobject.Hash(testNIK, testPepper)

	d.evidence.EXPECT().FindEvidence(gomock.Any(), wantHash).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), wantHash).Return(false, nil)
	d.breaker.EXPECT().State(gomock.Any()).Return(entity.BreakerClosed, nil)
	d.limiter.EXPECT().Allow(gomock.Any()).Return(true, nil)
	d.dukcapil.EXPECT().VerifyNIK(gomock.Any(), testNIK).Return(portout.DukcapilResult{Valid: true, Status: "ACTIVE", Ref: "ref-1"}, nil)
	d.breaker.EXPECT().RecordSuccess(gomock.Any()).Return(nil)
	d.cache.EXPECT().MarkVerified(gomock.Any(), wantHash, entity.CacheTTL).Return(nil)

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, wantHash, resp.NIKHmac)
	assert.Equal(t, string(entity.SourceVerified), resp.Source)
	assert.Equal(t, "3171**********22", resp.NIKMasked)
}

func TestValidate_CacheWriteFailureDoesNotFailTheRequest(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), gomock.Any()).Return(false, nil)
	d.breaker.EXPECT().State(gomock.Any()).Return(entity.BreakerClosed, nil)
	d.limiter.EXPECT().Allow(gomock.Any()).Return(true, nil)
	d.dukcapil.EXPECT().VerifyNIK(gomock.Any(), testNIK).Return(portout.DukcapilResult{Valid: true, Status: "ACTIVE"}, nil)
	d.breaker.EXPECT().RecordSuccess(gomock.Any()).Return(nil)
	d.cache.EXPECT().MarkVerified(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("redis down"))

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err, "the answer was already obtained; losing the cache entry must not fail it")
	assert.True(t, resp.Valid)
}

func TestValidate_BreakerStateUnavailable_AssumesClosed(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.Source(""), nil)
	d.cache.EXPECT().IsVerified(gomock.Any(), gomock.Any()).Return(false, nil)
	d.breaker.EXPECT().State(gomock.Any()).Return(entity.BreakerState(""), errors.New("redis down"))
	d.limiter.EXPECT().Allow(gomock.Any()).Return(true, nil)
	d.dukcapil.EXPECT().VerifyNIK(gomock.Any(), testNIK).Return(portout.DukcapilResult{Valid: true, Status: "ACTIVE"}, nil)
	d.breaker.EXPECT().RecordSuccess(gomock.Any()).Return(nil)
	d.cache.EXPECT().MarkVerified(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
	assert.True(t, resp.Valid)
}

func TestValidate_NeverEchoesTheRawNIK(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.evidence.EXPECT().FindEvidence(gomock.Any(), gomock.Any()).Return(entity.SourceRegistered, nil)

	resp, err := uc.Validate(context.Background(), req())
	require.NoError(t, err)
	assert.NotContains(t, resp.NIKMasked, "0000000000")
	assert.NotContains(t, resp.NIKHmac, testNIK)
}
