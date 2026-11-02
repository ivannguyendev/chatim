package mongostore

import (
	"errors"
	"fmt"
	"slices"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

const (
	writeConflictCode         = 112
	transientTransactionLabel = "TransientTransactionError"
	unknownCommitResultLabel  = "UnknownTransactionCommitResult"
)

var domainErrors = []error{
	apperr.ErrNotFound, apperr.ErrAlreadyExists, apperr.ErrFailedPrecondition, apperr.ErrInvalidArgument,
	apperr.ErrPermissionDenied, apperr.ErrUnauthenticated, apperr.ErrUnavailable,
}

func (s *Store) mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mongo.ErrNoDocuments):
		return apperr.ErrNotFound
	case isDomainError(err):
		return err
	case isRetryLater(err):
		return fmt.Errorf("%w: %w", apperr.ErrUnavailable, redact.Error(err, s.uri))
	case mongo.IsDuplicateKeyError(err):
		return fmt.Errorf("%w: duplicate key", apperr.ErrAlreadyExists)
	default:
		return fmt.Errorf("%w: %w", apperr.ErrUnavailable, redact.Error(err, s.uri))
	}
}

func isDomainError(err error) bool {
	return slices.ContainsFunc(domainErrors, func(target error) bool { return errors.Is(err, target) })
}

func isRetryLater(err error) bool {
	se, ok := errors.AsType[mongo.ServerError](err)
	return ok && (se.HasErrorLabel(transientTransactionLabel) || se.HasErrorLabel(unknownCommitResultLabel) || se.HasErrorCode(writeConflictCode))
}
