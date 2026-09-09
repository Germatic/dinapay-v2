package core

import "errors"

var (
	ErrInvalid             = errors.New("invalid request")
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrInProgress          = errors.New("operation in progress")
	ErrUnauthorized        = errors.New("unauthorized")
	ErrUnsupported         = errors.New("operation not supported")
	ErrInsufficientBalance = errors.New("insufficient balance")
)
