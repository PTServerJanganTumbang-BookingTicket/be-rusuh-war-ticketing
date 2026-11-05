// Package postgres adapts the shared Aurora cluster for evidence lookups.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/entity"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/outbound"
)

// EvidenceRepository reads stored proof of NIK verification.
//
// Read-only by design: services/user owns these tables. nik-verify holding a
// write path to them would make it possible to create verification evidence
// without a Dukcapil answer, which is the hole ADR-005 §8 closes with
// CHECK (verified_at IS NOT NULL).
type EvidenceRepository struct {
	pool DBConn
}

// NewEvidenceRepository returns the evidence adapter.
func NewEvidenceRepository(pool DBConn) portout.IEvidenceRepository {
	return &EvidenceRepository{pool: pool}
}

// findEvidence prefers a registered NIK over a saved one. Both are equally
// authoritative (ADR-005 §8); reporting the stronger of the two just makes the
// source field in the response more informative.
const findEvidence = `
SELECT source FROM (
    SELECT 'registered' AS source, 1 AS rank
      FROM accounts
     WHERE nik_hmac = $1 AND nik_verified_at IS NOT NULL
    UNION ALL
    SELECT 'saved' AS source, 2 AS rank
      FROM saved_niks
     WHERE nik_hmac = $1
) AS evidence
ORDER BY rank
LIMIT 1
`

// FindEvidence returns the strongest stored proof, or an empty Source when none
// exists. Absence is not an error — it is the normal path for a first-time NIK.
func (r *EvidenceRepository) FindEvidence(ctx context.Context, nikHmac string) (entity.Source, error) {
	var source string
	err := r.pool.QueryRow(ctx, findEvidence, nikHmac).Scan(&source)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return entity.Source(source), nil
}
