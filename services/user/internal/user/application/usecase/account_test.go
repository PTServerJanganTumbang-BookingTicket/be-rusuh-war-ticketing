package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

// wireOf marshals a response the way the handler will and returns the JSON
// alongside its top-level keys, so a test can assert the shape that reaches a
// client rather than the Go struct behind it.
func wireOf(t *testing.T, v any) (string, []string) {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return string(raw), keys
}

// profileAccount is an ordinary registered account: email confirmed, NIK bound.
func profileAccount() entity.Account {
	now := time.Date(2026, 11, 3, 9, 12, 0, 0, time.UTC)
	return entity.Account{
		ID:              testAccountID,
		Email:           "alex@example.com",
		EmailVerifiedAt: &now,
		NIKHmac:         "deadbeefdeadbeef",
		NIKMasked:       "3171********xx22",
		NIKVerifiedAt:   &now,
		DukcapilRef:     "DKP-99",
		CreatedAt:       now,
	}
}

func TestGetMyAccount_ReturnsEmailAndMaskedNIK(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindAccountByID(gomock.Any(), testAccountID).Return(profileAccount(), nil)

	out, err := uc.GetMyAccount(context.Background(), testAccountID)
	require.NoError(t, err)
	assert.Equal(t, "alex@example.com", out.Email)
	assert.Equal(t, "3171********xx22", out.NIKMasked)
}

// The account row holds a hash, a Dukcapil reference and three timestamps. None
// of them belong on a profile page, and a response built by copying the entity
// would have shipped all of them.
func TestGetMyAccount_LeaksNothingElseFromTheAccountRow(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindAccountByID(gomock.Any(), testAccountID).Return(profileAccount(), nil)

	out, err := uc.GetMyAccount(context.Background(), testAccountID)
	require.NoError(t, err)

	// Two fields on the wire, and exactly those two: a third one added later
	// fails here rather than reaching a client.
	raw, keys := wireOf(t, out)
	assert.Equal(t, []string{"email", "nik_masked"}, keys)
	for _, forbidden := range []string{"deadbeefdeadbeef", "DKP-99", "nik_hmac", "dukcapil", "verified_at", `"id"`} {
		assert.NotContains(t, raw, forbidden)
	}
}

// An account exists before registration finishes: a magic-link sign-in creates
// one with no NIK. The profile page has to render that account, so the field is
// empty rather than the call being an error.
func TestGetMyAccount_AccountWithoutABoundNIKReturnsAnEmptyMask(t *testing.T) {
	uc, d := newTestUsecase(t)
	account := entity.Account{ID: testAccountID, Email: "new@example.com"}
	d.repo.EXPECT().FindAccountByID(gomock.Any(), testAccountID).Return(account, nil)

	out, err := uc.GetMyAccount(context.Background(), testAccountID)
	require.NoError(t, err)
	assert.Equal(t, "new@example.com", out.Email)
	assert.Empty(t, out.NIKMasked)
	// Sent, not omitted: the page reads one shape for every account.
	raw, keys := wireOf(t, out)
	assert.Contains(t, raw, `"nik_masked":""`)
	assert.Equal(t, []string{"email", "nik_masked"}, keys)
}

// A signed token whose account is gone. 404 would be wrong twice: §1.8 defines
// only 200 and 401, and "me" exists exactly as long as the session's subject
// does — so it is the session that is unusable.
func TestGetMyAccount_MissingAccountIsUnauthorizedNotNotFound(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindAccountByID(gomock.Any(), testAccountID).
		Return(entity.Account{}, apperror.ErrNotFound("account not found"))

	_, err := uc.GetMyAccount(context.Background(), testAccountID)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 401, appErr.HTTPStatus)
	// TOKEN_INVALID, not TOKEN_EXPIRED: refreshing would mint another token for
	// the same missing account and the client would loop.
	assert.Equal(t, apperror.CodeTokenInvalid, appErr.ErrorCode)
}

// A database failure is not an authentication failure. Telling a customer their
// session expired during an outage sends them to log in again, which cannot
// work, instead of showing them a retryable error.
func TestGetMyAccount_RepositoryFailureIs500WithoutTheDriverMessage(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindAccountByID(gomock.Any(), testAccountID).
		Return(entity.Account{}, errors.New("pq: connection refused on 10.0.3.19:5432"))

	_, err := uc.GetMyAccount(context.Background(), testAccountID)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 500, appErr.HTTPStatus)
	assert.NotContains(t, appErr.Message, "pq:")
	assert.NotContains(t, appErr.Message, "10.0.3.19")
}

// The account comes from the token's subject and from nothing else, so the
// repository is asked for exactly the id the caller was authenticated as.
func TestGetMyAccount_ReadsTheAccountFromTheSessionSubject(t *testing.T) {
	uc, d := newTestUsecase(t)
	d.repo.EXPECT().FindAccountByID(gomock.Any(), "USR-someone-else").
		Return(entity.Account{ID: "USR-someone-else", Email: "other@example.com"}, nil)

	out, err := uc.GetMyAccount(context.Background(), "USR-someone-else")
	require.NoError(t, err)
	assert.Equal(t, "other@example.com", out.Email)
}
