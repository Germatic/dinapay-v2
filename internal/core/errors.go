package core

import (
	"errors"
	"fmt"

	contract "github.com/Germatic/dinapay-contracts/go/connectorcontract/failures"
)

var (
	ErrInvalid               = errors.New("invalid request")
	ErrNotFound              = errors.New("not found")
	ErrConflict              = errors.New("conflict")
	ErrWebhookAlreadyExists  = errors.New("webhook already exists")
	ErrInProgress            = errors.New("operation in progress")
	ErrUnauthorized          = errors.New("unauthorized")
	ErrUnsupported           = errors.New("operation not supported")
	ErrSimulationUnavailable = errors.New("simulation is not available for this transaction")
	ErrInsufficientBalance   = errors.New("insufficient balance")
	ErrProviderRejected      = errors.New("provider rejected request")
	ErrDestinationInUse      = errors.New("reusable destination already has an open payment")
	ErrExternalIDConflict    = errors.New("externalId already exists for merchant")
	ErrRouteUnsupported      = errors.New("requested payment route is not supported")
	ErrMissingRequiredData   = errors.New("required transaction data is missing")
	ErrForbiddenData         = errors.New("transaction data contains forbidden fields")
	ErrScreeningBlocked      = errors.New("transaction blocked by compliance policy")
	ErrScreeningReview       = errors.New("transaction requires compliance review")
)

// MissingRequiredDataError reports the public contract paths required by the
// active transaction-data policy. It intentionally carries no provider data.
type MissingRequiredDataError struct {
	Fields []string
}

func (e *MissingRequiredDataError) Error() string { return ErrMissingRequiredData.Error() }
func (e *MissingRequiredDataError) Unwrap() error { return ErrMissingRequiredData }

// ForbiddenDataError reports public contract paths that an active policy does
// not permit the caller to submit.
type ForbiddenDataError struct {
	Fields []string
}

func (e *ForbiddenDataError) Error() string { return ErrForbiddenData.Error() }
func (e *ForbiddenDataError) Unwrap() error { return ErrForbiddenData }

// ProviderRejectedError carries a connector-normalized failure. ProviderFailure
// is internal-only and must not be returned in merchant-facing responses.
type ProviderRejectedError struct {
	Message         string
	Failure         *contract.Failure
	ProviderFailure *contract.ProviderFailure
}

func (e *ProviderRejectedError) Error() string { return e.Message }
func (e *ProviderRejectedError) Unwrap() error { return ErrProviderRejected }

type AliasResolutionError struct {
	Message   string
	Permanent bool
}

func (e *AliasResolutionError) Error() string { return e.Message }

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
