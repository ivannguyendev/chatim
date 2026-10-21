package wsclient

import (
	"errors"
	"fmt"

	"github.com/lxzan/gws"
)

const (
	CodeUnauthenticated uint16 = 4001
	CodeAuthTimeout     uint16 = 4002
)

var (
	ErrUnauthenticated = errors.New("wsclient: unauthenticated")
	ErrAuthTimeout     = errors.New("wsclient: auth timeout")
	ErrClosed          = errors.New("wsclient: closed")
)

type CloseError struct {
	Code   uint16
	Reason string
	kind   error
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("%v (code %d)", e.kind, e.Code)
}

func (e *CloseError) Unwrap() error { return e.kind }

func newCloseError(code uint16, reason string) *CloseError {
	kind := ErrClosed
	switch code {
	case CodeUnauthenticated:
		kind = ErrUnauthenticated
	case CodeAuthTimeout:
		kind = ErrAuthTimeout
	}
	return &CloseError{Code: code, Reason: reason, kind: kind}
}

type statusCode interface {
	error
	Uint16() uint16
}

func closeErrorOf(err error) error {
	if closeErr, ok := errors.AsType[*gws.CloseError](err); ok {
		return newCloseError(closeErr.Code, string(closeErr.Reason))
	}
	if code, ok := errors.AsType[statusCode](err); ok {
		return newCloseError(code.Uint16(), "")
	}
	return fmt.Errorf("%w: %w", ErrClosed, err)
}
