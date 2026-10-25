package coreroute

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrNoRoute          = status.Error(codes.Unavailable, "no live core to route to")
	ErrDial             = errors.New("dial core")
	ErrTooManyAddresses = fmt.Errorf("more than %d core addresses", MaxAddresses)

	errClosed = status.Error(codes.Unavailable, "core routes closed")
)

type unavailableError struct {
	cause error
}

func unavailable(cause error) error { return &unavailableError{cause: cause} }

func (e *unavailableError) Error() string { return e.GRPCStatus().Err().Error() }

func (e *unavailableError) GRPCStatus() *status.Status {
	return status.New(codes.Unavailable, e.cause.Error())
}

func (e *unavailableError) Unwrap() error { return e.cause }
