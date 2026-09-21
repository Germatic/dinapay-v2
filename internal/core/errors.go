package core

import (
	"errors"
	"fmt"
)

var (
	ErrInvalid             = errors.New("invalid request")
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrInProgress          = errors.New("operation in progress")
	ErrUnauthorized        = errors.New("unauthorized")
	ErrUnsupported         = errors.New("operation not supported")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrProviderRejected    = errors.New("provider rejected request")
	ErrDestinationInUse    = errors.New("reusable destination already has an open payment")
	ErrExternalIDConflict  = errors.New("externalId already exists for merchant")
	ErrRouteUnsupported    = errors.New("requested payment route is not supported")
)

type ValidationError struct {
	Field   string
	Rule    string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("%s is invalid", e.Field)
}

func (e *ValidationError) Unwrap() error { return ErrInvalid }

func Required(field string) error {
	return &ValidationError{Field: field, Rule: "required", Message: field + " is required"}
}
