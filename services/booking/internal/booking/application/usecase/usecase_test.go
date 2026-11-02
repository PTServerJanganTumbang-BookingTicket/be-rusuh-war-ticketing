package usecase_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
)

// validRequest returns a minimal valid CreateBookingRequest for usecase tests.
func validRequest() dto.CreateBookingRequest {
	return dto.CreateBookingRequest{
		EventID: "evt_rusuh_2026",
		Tickets: []dto.TicketRequest{
			{CategoryID: "cat_vip", NIK: "3171000000000001", HolderEmail: "a@example.com"},
		},
		ContactEmail: "contact@example.com",
		TermsVersion: "v1",
	}
}

// assertCode asserts err unwraps to an *apperror.AppError carrying the given code.
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *apperror.AppError
	assert.True(t, errors.As(err, &appErr), "expected *apperror.AppError, got %v", err)
	assert.Equal(t, code, appErr.Code)
}
