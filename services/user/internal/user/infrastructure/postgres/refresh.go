package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

const insertRefreshToken = `
INSERT INTO refresh_tokens (id, account_id, family_id, token_hash, issued_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
`

// CreateRefreshToken opens a new rotation chain.
func (r *UserRepository) CreateRefreshToken(ctx context.Context, t entity.RefreshToken) error {
	_, err := r.pool.Exec(ctx, insertRefreshToken,
		t.ID, t.AccountID, t.FamilyID, t.TokenHash, t.IssuedAt, t.ExpiresAt)
	if err != nil {
		return apperror.ErrInternal("failed to store refresh token")
	}
	return nil
}

// redeemRefreshToken spends a token in one statement.
//
// Every precondition is in the WHERE clause, so the UPDATE either redeems the
// token or matches nothing. Two concurrent refreshes from the same client — a
// retry on a flaky connection, say — must not both succeed, or two live chains
// descend from one token and the replay detector below starts firing on
// legitimate traffic.
const redeemRefreshToken = `
UPDATE refresh_tokens
   SET used_at = now()
 WHERE token_hash = $1
   AND used_at IS NULL
   AND revoked_at IS NULL
   AND expires_at > now()
RETURNING id, account_id, family_id
`

// RotateRefreshToken redeems a token and issues its replacement atomically.
//
// On a replay — a token that was already redeemed — it revokes the entire
// family and returns ErrRefreshTokenReplayed. The family goes, not just the
// replayed link: a replay means the credential leaked, and whoever holds the
// newest link in the chain may be the attacker rather than the customer
// (OAuth 2.0 BCP §4.13). Ending the session is the cheap, correct response.
func (r *UserRepository) RotateRefreshToken(
	ctx context.Context, presentedHash string, replacement entity.RefreshToken,
) (entity.RefreshToken, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return entity.RefreshToken{}, apperror.ErrInternal("failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var redeemedID, accountID, familyID string
	err = tx.QueryRow(ctx, redeemRefreshToken, presentedHash).Scan(&redeemedID, &accountID, &familyID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Nothing was redeemable. Distinguish a replay from an unknown or
		// expired token, because only a replay is evidence of theft.
		return entity.RefreshToken{}, r.classifyFailedRotation(ctx, presentedHash)
	}
	if err != nil {
		return entity.RefreshToken{}, apperror.ErrInternal("failed to redeem refresh token")
	}

	// The chain's owner comes from the redeemed row, never from the caller.
	replacement.AccountID = accountID
	replacement.FamilyID = familyID

	if _, err := tx.Exec(ctx, insertRefreshToken,
		replacement.ID, replacement.AccountID, replacement.FamilyID,
		replacement.TokenHash, replacement.IssuedAt, replacement.ExpiresAt,
	); err != nil {
		return entity.RefreshToken{}, apperror.ErrInternal("failed to issue refresh token")
	}

	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET replaced_by = $1 WHERE id = $2`, replacement.ID, redeemedID,
	); err != nil {
		return entity.RefreshToken{}, apperror.ErrInternal("failed to link refresh token")
	}

	if err := tx.Commit(ctx); err != nil {
		return entity.RefreshToken{}, apperror.ErrInternal("failed to commit rotation")
	}
	return replacement, nil
}

// classifyFailedRotation decides why a rotation found nothing to redeem.
//
// Runs on the failure path only, outside the caller's transaction, because
// revoking the family must survive that transaction rolling back.
func (r *UserRepository) classifyFailedRotation(ctx context.Context, presentedHash string) error {
	var familyID string
	var used bool
	err := r.pool.QueryRow(ctx,
		`SELECT family_id, used_at IS NOT NULL FROM refresh_tokens WHERE token_hash = $1`, presentedHash,
	).Scan(&familyID, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.ErrNotFound("refresh token not found")
	}
	if err != nil {
		return apperror.ErrInternal("failed to inspect refresh token")
	}
	if !used {
		// Known but revoked or expired. Not a replay: no second holder is
		// implied, so the customer simply signs in again.
		return apperror.ErrNotFound("refresh token is no longer valid")
	}
	if err := r.RevokeRefreshFamily(ctx, familyID); err != nil {
		return err
	}
	return portout.ErrRefreshTokenReplayed
}

// RevokeRefreshFamily invalidates every live token in a rotation chain.
func (r *UserRepository) RevokeRefreshFamily(ctx context.Context, familyID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`, familyID)
	if err != nil {
		return apperror.ErrInternal("failed to revoke refresh token family")
	}
	return nil
}
