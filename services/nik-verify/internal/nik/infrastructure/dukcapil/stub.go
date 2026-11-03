package dukcapil

import (
	"context"
	"strings"

	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/outbound"
)

// StubAdapter is the no-network implementation for local development and CI.
//
// It is selected by DUKCAPIL_MODE=stub and never by default, so a deployment
// that forgets to configure the real endpoint fails at startup rather than
// quietly verifying every NIK it is given.
//
// Unlike a blanket "everything is valid" stub, this one can produce all three
// outcomes, so the rejection and outage paths are reachable without a real
// endpoint: a NIK ending 0000 is rejected, one ending 9999 simulates an outage.
type StubAdapter struct{}

// NewStubAdapter returns a StubAdapter.
func NewStubAdapter() portout.IDukcapilAdapter { return &StubAdapter{} }

type stubOutage struct{}

func (stubOutage) Error() string { return "dukcapil stub: simulated outage" }

// VerifyNIK answers from the NIK's last four digits.
func (s *StubAdapter) VerifyNIK(_ context.Context, nik string) (portout.DukcapilResult, error) {
	switch {
	case strings.HasSuffix(nik, "9999"):
		return portout.DukcapilResult{}, stubOutage{}
	case strings.HasSuffix(nik, "0000"):
		return portout.DukcapilResult{Valid: false, Status: "NOT_FOUND"}, nil
	default:
		return portout.DukcapilResult{Valid: true, Status: "ACTIVE", Ref: "stub-" + nik[len(nik)-4:]}, nil
	}
}
