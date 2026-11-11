package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// UserRepository is the Aurora-backed IUserRepository.
type UserRepository struct {
	pool DBConn
}

// NewUserRepository returns the repository port backed by Postgres.
func NewUserRepository(pool DBConn) portout.IUserRepository {
	return &UserRepository{pool: pool}
}

const accountColumns = `id, email, email_verified_at, COALESCE(nik_hmac, ''), COALESCE(nik_masked, ''),
       nik_verified_at, COALESCE(dukcapil_ref, ''), created_at`

func scanAccount(row pgx.Row) (entity.Account, error) {
	var a entity.Account
	err := row.Scan(&a.ID, &a.Email, &a.EmailVerifiedAt, &a.NIKHmac, &a.NIKMasked,
		&a.NIKVerifiedAt, &a.DukcapilRef, &a.CreatedAt)
	return a, err
}

// FindAccountByEmail looks an account up by address, case-insensitively, to
// match the lower(email) unique index.
func (r *UserRepository) FindAccountByEmail(ctx context.Context, email string) (entity.Account, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts WHERE lower(email) = lower($1)`, email)
	a, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Account{}, apperror.ErrNotFound("account not found")
	}
	if err != nil {
		return entity.Account{}, apperror.ErrInternal("failed to read account")
	}
	return a, nil
}

// FindAccountByID looks an account up by its identifier.
func (r *UserRepository) FindAccountByID(ctx context.Context, id string) (entity.Account, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = $1`, id)
	a, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Account{}, apperror.ErrNotFound("account not found")
	}
	if err != nil {
		return entity.Account{}, apperror.ErrInternal("failed to read account")
	}
	return a, nil
}

const insertAccount = `
INSERT INTO accounts (id, email, email_verified_at, nik_hmac, nik_masked, nik_verified_at, dukcapil_ref, created_at)
VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, NULLIF($7, ''), $8)
`

// RegisterAccount writes the account, its consent record, its verification
// token and the account.verify event in one transaction.
//
// Four statements, one commit. Every partial outcome is a defect nothing
// downstream can detect — see portout.Registration for the enumeration.
func (r *UserRepository) RegisterAccount(ctx context.Context, reg portout.Registration) (entity.Account, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return entity.Account{}, apperror.ErrInternal("failed to begin transaction")
	}
	// Rollback after a successful Commit is a no-op, so deferring this
	// unconditionally guarantees no transaction is left open on any path.
	defer func() { _ = tx.Rollback(ctx) }()

	a := reg.Account
	if _, err := tx.Exec(ctx, insertAccount,
		a.ID, a.Email, a.EmailVerifiedAt, a.NIKHmac, a.NIKMasked, a.NIKVerifiedAt, a.DukcapilRef, a.CreatedAt,
	); err != nil {
		return entity.Account{}, mapAccountWriteError(err)
	}

	c := reg.Consent
	if _, err := tx.Exec(ctx,
		`INSERT INTO consent_records (id, account_id, consented_at) VALUES ($1, $2, $3)`,
		c.ID, c.AccountID, c.ConsentedAt,
	); err != nil {
		return entity.Account{}, apperror.ErrInternal("failed to record consent")
	}

	if err := insertMagicToken(ctx, tx, reg.VerifyToken); err != nil {
		return entity.Account{}, apperror.ErrInternal("failed to store verification token")
	}

	if err := enqueue(ctx, tx, reg.Event); err != nil {
		return entity.Account{}, apperror.ErrInternal("failed to queue verification email")
	}

	if err := tx.Commit(ctx); err != nil {
		return entity.Account{}, apperror.ErrInternal("failed to commit registration")
	}
	return a, nil
}

func mapAccountWriteError(err error) error {
	if constraint, ok := uniqueViolation(err); ok {
		switch constraint {
		case indexAccountNIK:
			return apperror.
				ErrConflict("this NIK is already registered to an account").
				WithCode(apperror.CodeNIKAlreadyRegistered)
		case indexAccountEmail:
			return apperror.
				ErrConflict("an account already exists for this email").
				WithCode(apperror.CodeEmailAlreadyUsed)
		}
		return apperror.ErrConflict("account already exists")
	}
	if constraint, ok := checkViolation(err); ok && constraint == "accounts_nik_all_or_nothing" {
		// A bug, not a user error: the three NIK columns must travel together.
		return apperror.ErrInternal("account NIK fields are inconsistent")
	}
	return apperror.ErrInternal("failed to create account")
}

// MarkEmailVerified records that the address has been confirmed. Idempotent:
// re-confirming keeps the original timestamp.
func (r *UserRepository) MarkEmailVerified(ctx context.Context, accountID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE accounts SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now() WHERE id = $1`,
		accountID)
	if err != nil {
		return apperror.ErrInternal("failed to mark email verified")
	}
	return nil
}

// HasConsent reports whether the account has ever recorded consent.
func (r *UserRepository) HasConsent(ctx context.Context, accountID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM consent_records WHERE account_id = $1)`, accountID).Scan(&exists)
	if err != nil {
		return false, apperror.ErrInternal("failed to check consent")
	}
	return exists, nil
}
