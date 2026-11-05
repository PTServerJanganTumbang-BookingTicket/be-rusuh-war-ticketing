package postgres

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

// CreateSavedNIKWithinCap inserts an entry while enforcing the BR-12 cap.
//
// The count and the insert run inside one transaction that first takes
// SELECT … FOR UPDATE on the owning account row. That lock is the whole
// mechanism: BR-12 is a row count, which no constraint can express, so two
// concurrent adds would otherwise both read four entries and both insert.
// Locking the account serialises adds per account without blocking anyone else.
func (r *UserRepository) CreateSavedNIKWithinCap(ctx context.Context, n entity.SavedNIK, maxEntries int) (entity.SavedNIK, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return entity.SavedNIK{}, apperror.ErrInternal("failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var accountExists string
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE id = $1 FOR UPDATE`, n.AccountID).Scan(&accountExists); err != nil {
		return entity.SavedNIK{}, apperror.ErrNotFound("account not found")
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saved_niks WHERE account_id = $1`, n.AccountID).Scan(&count); err != nil {
		return entity.SavedNIK{}, apperror.ErrInternal("failed to count saved NIKs")
	}
	if count >= maxEntries {
		return entity.SavedNIK{}, apperror.
			ErrConflict("the saved NIK list is full; remove one first").
			WithCode(apperror.CodeMaxSavedNIKsReached)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO saved_niks (id, account_id, nik_hmac, nik_masked, label, verified_at, dukcapil_ref)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		n.ID, n.AccountID, n.NIKHmac, n.NIKMasked, n.Label, n.VerifiedAt, n.DukcapilRef)
	if err != nil {
		if constraint, ok := uniqueViolation(err); ok && constraint == indexSavedNIKPerAcc {
			return entity.SavedNIK{}, apperror.
				ErrConflict("this NIK is already in your list").
				WithCode(apperror.CodeNIKAlreadySaved)
		}
		return entity.SavedNIK{}, apperror.ErrInternal("failed to save NIK")
	}

	if err := tx.Commit(ctx); err != nil {
		return entity.SavedNIK{}, apperror.ErrInternal("failed to commit saved NIK")
	}
	return n, nil
}

// FindSavedNIKsByAccountID returns the account's entries, newest first.
func (r *UserRepository) FindSavedNIKsByAccountID(ctx context.Context, accountID string) ([]entity.SavedNIK, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, nik_hmac, nik_masked, label, verified_at, dukcapil_ref
		  FROM saved_niks
		 WHERE account_id = $1
		 ORDER BY created_at DESC`, accountID)
	if err != nil {
		return nil, apperror.ErrInternal("failed to read saved NIKs")
	}
	defer rows.Close()

	result := make([]entity.SavedNIK, 0, 5)
	for rows.Next() {
		var n entity.SavedNIK
		if err := rows.Scan(&n.ID, &n.AccountID, &n.NIKHmac, &n.NIKMasked, &n.Label, &n.VerifiedAt, &n.DukcapilRef); err != nil {
			return nil, apperror.ErrInternal("failed to read saved NIKs")
		}
		result = append(result, n)
	}
	if err := rows.Err(); err != nil {
		return nil, apperror.ErrInternal("failed to read saved NIKs")
	}
	return result, nil
}

// DeleteSavedNIK removes an entry owned by this account.
//
// account_id is in the WHERE clause, not checked beforehand: ownership is part
// of the statement, so there is no window between the check and the delete and
// no path that deletes another account's row.
func (r *UserRepository) DeleteSavedNIK(ctx context.Context, accountID, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM saved_niks WHERE id = $1 AND account_id = $2`, id, accountID)
	if err != nil {
		return apperror.ErrInternal("failed to delete saved NIK")
	}
	if tag.RowsAffected() == 0 {
		// Missing and not-yours are the same answer: a 403 would confirm the
		// ID exists (§9).
		return apperror.ErrNotFound("saved NIK not found")
	}
	return nil
}
