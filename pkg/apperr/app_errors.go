package apperr

import "errors"

var (
	ErrNotFound           = errors.New("not found")
	ErrAlreadyExists      = errors.New("already exists")
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrFailedPrecondition = errors.New("failed precondition")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrUnavailable        = errors.New("dependency unavailable")
)
