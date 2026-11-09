package usecase

import (
	"testing"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
)

// Join stores device_hash; booking's grantauth recomputes it from X-Device-Id. One byte of difference locks every
// guest out of the ticket page with DEVICE_MISMATCH, so both sides must agree.
func TestJoinsDeviceHashEqualsTheOneGrantauthChecks(t *testing.T) {
	pepper := []byte("shared-pepper")
	for _, id := range []string{"d3f1a9c4b70e42f88a1c5e6072b9d4af", "abc_DEF-123456789", ""} {
		if got, want := (caller{pepper: pepper}).deviceHash(id), grantauth.DeviceHash(pepper, id); got != want {
			t.Errorf("device %q: join hash %q, grantauth hash %q", id, got, want)
		}
	}
}
