// Package dukcapil provides a stub implementation of the Dukcapil (Directorate General
// of Population and Civil Registration) verification adapter for local development and
// the POC Lambda deployment.
//
// Production must replace this stub with a real HTTP client to the Dukcapil API.
// The stub accepts any 16-digit numeric NIK as valid and returns ACTIVE status.
package dukcapil

import (
	"context"
	"fmt"

	portout "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// StubAdapter is a no-network implementation that always returns a successful
// verification for syntactically valid NIKs. It satisfies IDukcapilAdapter.
type StubAdapter struct{}

// NewStubAdapter returns a StubAdapter ready for use.
func NewStubAdapter() portout.IDukcapilAdapter {
	return &StubAdapter{}
}

// VerifyNIK accepts any 16-character string as a valid, active NIK.
// It returns an error only on invalid input (wrong length), never for network reasons.
func (s *StubAdapter) VerifyNIK(_ context.Context, nik string) (portout.DukcapilResult, error) {
	if len(nik) != 16 {
		return portout.DukcapilResult{}, fmt.Errorf("stub: NIK must be exactly 16 digits, got %d", len(nik))
	}
	return portout.DukcapilResult{
		Valid:  true,
		Name:   "VERIFIED USER",
		Status: "ACTIVE",
		Ref:    "stub-ref-" + nik[:4],
	}, nil
}
