package api

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	msgInvalidProof  = "invalid dpop proof"
	msgRequestFail   = "request failed"
	clientMessageMax = 200
)

func (s *server) connectError(ctx context.Context, procedure string, err error) *connect.Error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	case errors.Is(err, auth.ErrPasswordChangeRequired):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("password change required"))
	case errors.Is(err, apperr.ErrFailedPrecondition):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New(detailOf(err, apperr.ErrFailedPrecondition)))
	case errors.Is(err, apperr.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New(detailOf(err, apperr.ErrPermissionDenied)))
	case errors.Is(err, auth.ErrSessionInvalid):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("session is not active"))
	case errors.Is(err, apperr.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	case errors.Is(err, apperr.ErrInvalidArgument):
		return connect.NewError(connect.CodeInvalidArgument, errors.New(invalidArgumentMessage(err)))
	case errors.Is(err, apperr.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	case errors.Is(err, apperr.ErrAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("already exists"))
	case errors.Is(err, apperr.ErrResourceExhausted):
		ce := connect.NewError(connect.CodeResourceExhausted, errors.New("too many attempts, retry later"))
		ce.Meta().Set("Retry-After", "1")
		return ce
	case errors.Is(err, apperr.ErrUnavailable):
		s.opts.Log.WarnContext(ctx, "iam request unavailable", "procedure", procedure, "err", err.Error())
		return errUnavailable("temporarily unavailable")
	default:
		s.opts.Log.ErrorContext(ctx, "iam request failed", "procedure", procedure, "err", err.Error())
		return connect.NewError(connect.CodeInternal, errors.New(msgRequestFail))
	}
}

func invalidArgumentMessage(err error) string {
	if errors.Is(err, auth.ErrProofReplayed) {
		return msgInvalidProof
	}
	return detailOf(err, apperr.ErrInvalidArgument)
}

func detailOf(err, kind error) string {
	if detail, ok := strings.CutPrefix(err.Error(), kind.Error()+": "); ok && detail != "" {
		return clip(detail)
	}
	return kind.Error()
}

func clip(msg string) string {
	if len(msg) <= clientMessageMax {
		return msg
	}
	n := clientMessageMax
	for n > 0 && !utf8.RuneStart(msg[n]) {
		n--
	}
	return msg[:n]
}
