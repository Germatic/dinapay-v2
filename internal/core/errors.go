package core

import "errors"

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInProgress   = errors.New("operation in progress")
	ErrUnauthorized = errors.New("unauthorized")
)
