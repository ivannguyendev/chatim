package wstest

import (
	"errors"
	"fmt"

	"github.com/lxzan/gws"
)

const (
	CodeUnauthenticated uint16 = 4001
	CodeAuthTimeout     uint16 = 4002
)

var ErrClosed = errors.New("wstest: connection closed")

type CloseError struct {
	Code   uint16
	Reason string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("wstest: connection closed with code %d", e.Code)
}

func (e *CloseError) Unwrap() error { return ErrClosed }

type statusCode interface {
	error
	Uint16() uint16
}

func closeErrorOf(err error) error {
	if closeErr, ok := errors.AsType[*gws.CloseError](err); ok {
		return &CloseError{Code: closeErr.Code, Reason: string(closeErr.Reason)}
	}
	if code, ok := errors.AsType[statusCode](err); ok {
		return &CloseError{Code: code.Uint16()}
	}
	if err == nil {
		return ErrClosed
	}
	return fmt.Errorf("%w: %w", ErrClosed, err)
}
