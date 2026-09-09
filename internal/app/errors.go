package app

import (
	"errors"

	"github.com/Germatic/dinapay-v2/internal/core"
)

var (
	ErrInvalid      = errors.New("invalid request")
	ErrUnauthorized = core.ErrUnauthorized
	ErrNotFound     = core.ErrNotFound
	ErrConflict     = core.ErrConflict
	ErrInProgress   = core.ErrInProgress
)
