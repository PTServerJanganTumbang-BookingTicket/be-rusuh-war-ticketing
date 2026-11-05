package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

const insertMagicTokenSQL = `
INSERT INTO magic_tokens (id, account_id, email, token_hash, purpose, created_at, expires_at)
VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7)
`

// insertMagicToken stores the HASH of an emailed link, never the link itself.
// Takes a transaction handle so it can join a larger write.
func insertMagicToken(ctx context.Context, tx pgx.Tx, t entity.MagicToken) error {
	_, err := tx.Exec(ctx, insertMagicTokenSQL,
		t.ID, t.AccountID, t.Email, t.TokenHash, string(t.Purpose), t.CreatedAt, t.ExpiresAt)
	return err
}

// SaveMagicTokenWithEvent stores the token and queues its email together.
//
// One transaction because the two halves are useless apart: a token with no
// queued email is a link nobody receives, and a queued email with no token is
// a link that cannot be redeemed.
func (r *UserRepository) SaveMagicTokenWithEvent(ctx context.Context, t entity.MagicToken, e entity.OutboxEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return apperror.ErrInternal("failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := insertMagicToken(ctx, tx, t); err != nil {
		return apperror.ErrInternal("failed to store token")
	}
	if err := enqueue(ctx, tx, e); err != nil {
		return apperror.ErrInternal("failed to queue email")
	}
	if err := tx.Commit(ctx); err != nil {
		return apperror.ErrInternal("failed to commit token")
	}
	return nil
}

// consumeMagicToken claims the token in a single statement.
//
// The WHERE clause carries every precondition — unused, unexpired, right
// purpose — so the UPDATE either claims the token or matches nothing. Reading
// then updating would let two clicks on one emailed link both pass the check
// and both get a session, and under a flash crowd a double-click is the normal
// case rather than a rare one.
//
// Matching on purpose is what keeps a verification link from being redeemed as
// a login link.
const consumeMagicToken = `
UPDATE magic_tokens
   SET used_at = now()
 WHERE token_hash = $1
   AND purpose = $2
   AND used_at IS NULL
   AND expires_at > now()
RETURNING id, COALESCE(account_id, ''), email, token_hash, purpose, created_at, expires_at, used_at
`

// ConsumeMagicToken atomically marks a usable token as used and returns it.
func (r *UserRepository) ConsumeMagicToken(ctx context.Context, tokenHash string, purpose entity.Purpose) (entity.MagicToken, error) {
	var t entity.MagicToken
	var p string
	err := r.pool.QueryRow(ctx, consumeMagicToken, tokenHash, string(purpose)).
		Scan(&t.ID, &t.AccountID, &t.Email, &t.TokenHash, &p, &t.CreatedAt, &t.ExpiresAt, &t.UsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown, expired, already used and wrong purpose are
		// indistinguishable on purpose: telling them apart confirms which
		// addresses have had links issued.
		return entity.MagicToken{}, apperror.ErrNotFound("token is not usable")
	}
	if err != nil {
		return entity.MagicToken{}, apperror.ErrInternal("failed to consume token")
	}
	t.Purpose = entity.Purpose(p)
	return t, nil
}
