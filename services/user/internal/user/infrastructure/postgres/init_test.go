package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
)

func TestUniqueViolation_Match(t *testing.T) {
	pgErr := &pgconn.PgError{Code: codeUniqueViolation, ConstraintName: "accounts_email_key"}
	name, ok := uniqueViolation(pgErr)
	assert.True(t, ok)
	assert.Equal(t, "accounts_email_key", name)
}

func TestUniqueViolation_WrongCode(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23000", ConstraintName: "some_constraint"}
	_, ok := uniqueViolation(pgErr)
	assert.False(t, ok)
}

func TestUniqueViolation_NotPgError(t *testing.T) {
	_, ok := uniqueViolation(errors.New("other error"))
	assert.False(t, ok)
}

func TestCheckViolation_Match(t *testing.T) {
	pgErr := &pgconn.PgError{Code: codeCheckViolation, ConstraintName: "accounts_nik_all_or_nothing"}
	name, ok := checkViolation(pgErr)
	assert.True(t, ok)
	assert.Equal(t, "accounts_nik_all_or_nothing", name)
}

func TestCheckViolation_WrongCode(t *testing.T) {
	_, ok := checkViolation(&pgconn.PgError{Code: codeUniqueViolation})
	assert.False(t, ok)
}

func TestMapAccountWriteError_EmailConflict(t *testing.T) {
	err := mapAccountWriteError(&pgconn.PgError{Code: codeUniqueViolation, ConstraintName: indexAccountEmail})
	var ae *apperror.AppError
	assert.ErrorAs(t, err, &ae)
	assert.Equal(t, apperror.CodeEmailAlreadyUsed, ae.ErrorCode)
}

func TestMapAccountWriteError_NIKConflict(t *testing.T) {
	err := mapAccountWriteError(&pgconn.PgError{Code: codeUniqueViolation, ConstraintName: indexAccountNIK})
	var ae *apperror.AppError
	assert.ErrorAs(t, err, &ae)
	assert.Equal(t, apperror.CodeNIKAlreadyRegistered, ae.ErrorCode)
}

func TestMapAccountWriteError_OtherUnique(t *testing.T) {
	err := mapAccountWriteError(&pgconn.PgError{Code: codeUniqueViolation, ConstraintName: "other_key"})
	assert.Error(t, err)
}

func TestMapAccountWriteError_NIKAllOrNothing(t *testing.T) {
	err := mapAccountWriteError(&pgconn.PgError{Code: codeCheckViolation, ConstraintName: "accounts_nik_all_or_nothing"})
	assert.Error(t, err)
}

func TestMapAccountWriteError_UnknownError(t *testing.T) {
	err := mapAccountWriteError(errors.New("some db error"))
	assert.Error(t, err)
}
