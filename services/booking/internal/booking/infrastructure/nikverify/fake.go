package nikverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/nikformat"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// Fake is the local stand-in for nik-verify (APP_ENV=local without NIK_VERIFY_FUNCTION_NAME): it follows nik-verify's
// Dukcapil stub, so a NIK ending 0000 is rejected and one ending 9999 simulates an outage. The hash is not the real
// HMAC (it has no pepper): local data only, and config refuses to run it in a deployed environment.
type Fake struct{}

// NewFake returns the local stand-in.
func NewFake() *Fake { return &Fake{} }

var _ outbound.NIKVerifier = (*Fake)(nil)

// Validate answers from the NIK's last four digits.
func (*Fake) Validate(_ context.Context, nik string) (domain.NIKCheck, error) {
	n := nikformat.Normalize(nik)
	if !nikformat.IsValidFormat(n) {
		return domain.NIKCheck{}, domain.ErrNIKUnavailable
	}
	sum := sha256.Sum256([]byte("local-fake:" + n))
	c := domain.NIKCheck{NIKHmac: hex.EncodeToString(sum[:]), NIKMasked: n[:4] + strings.Repeat("*", 10) + n[14:], Source: "verified"}
	switch {
	case strings.HasSuffix(n, "9999"):
		return domain.NIKCheck{}, domain.ErrNIKUnavailable
	case strings.HasSuffix(n, "0000"):
		c.Status = "NOT_FOUND"
	default:
		c.Valid, c.Status = true, "ACTIVE"
	}
	return c, nil
}
