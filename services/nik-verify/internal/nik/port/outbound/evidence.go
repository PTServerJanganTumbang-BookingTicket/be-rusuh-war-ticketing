package outbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/entity"
)

// IEvidenceRepository looks up stored proof that a NIK was verified.
//
// Reads only. nik-verify never writes to the identity tables; services/user
// owns those. Checking here is what makes NIK_Terdaftar and Daftar NIK Teman
// pay for themselves — a NIK already proven costs no Dukcapil call (NFR-12).
type IEvidenceRepository interface {
	// FindEvidence returns the strongest stored proof for a NIK hash, or an
	// empty Source when none exists. It must not error merely because nothing
	// was found.
	FindEvidence(ctx context.Context, nikHmac string) (entity.Source, error)
}
