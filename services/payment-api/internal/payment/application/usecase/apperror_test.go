package usecase

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
)

// assertAppError checks the HTTP status and the Appendix A error code together.
// Clients switch on the code, so a right status with a wrong code is still a
// broken response.
func assertAppError(t *testing.T, err error, status int, code string) {
	t.Helper()
	require.Error(t, err)
	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr), "expected *apperror.AppError, got %T", err)
	assert.Equal(t, status, appErr.HTTPStatus)
	assert.Equal(t, code, appErr.ErrorCode)
}
