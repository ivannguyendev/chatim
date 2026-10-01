package grpcserver

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var domainCodes = []struct {
	err  error
	code codes.Code
}{
	{apperr.ErrNotFound, codes.NotFound},
	{apperr.ErrAlreadyExists, codes.AlreadyExists},
	{apperr.ErrInvalidArgument, codes.InvalidArgument},
	{apperr.ErrFailedPrecondition, codes.FailedPrecondition},
	{apperr.ErrPermissionDenied, codes.PermissionDenied},
	{apperr.ErrUnauthenticated, codes.Unauthenticated},
	{apperr.ErrUnavailable, codes.Unavailable},
	{apperr.ErrResourceExhausted, codes.ResourceExhausted},
}

func ToStatus(err error) error {
	if err == nil {
		return nil
	}
	if s, ok := err.(interface{ GRPCStatus() *status.Status }); ok {
		return s.GRPCStatus().Err()
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	}
	for _, d := range domainCodes {
		if errors.Is(err, d.err) {
			return status.Error(d.code, d.err.Error())
		}
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.DeadlineExceeded:
			return status.Error(codes.DeadlineExceeded, "deadline exceeded")
		case codes.Canceled:
			return status.Error(codes.Canceled, "request canceled")
		case codes.Unavailable:
			return status.Error(codes.Unavailable, "dependency unavailable")
		default:
		}
	}
	return status.Error(codes.Internal, "internal error")
}
