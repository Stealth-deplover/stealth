package repository

import (
	"errors"
)

var (
	ErrNotFound             = errors.New("not found")
	ErrConflict             = errors.New("conflict")
	ErrReferenceViolation   = errors.New("reference violation")
	ErrBackupTooLarge       = errors.New("database backup is too large")
	ErrInvalidBackup        = errors.New("invalid database backup")
	ErrForbidden            = errors.New("forbidden")
	ErrConfirmationRequired = errors.New("confirmation required")
	ErrRegistrationDisabled = errors.New("registration disabled")
	ErrBootstrapRequired    = errors.New("bootstrap is required")
	ErrBootstrapSealed      = errors.New("bootstrap is sealed")
	ErrInvalidBootstrapCode = errors.New("invalid bootstrap code")
)
