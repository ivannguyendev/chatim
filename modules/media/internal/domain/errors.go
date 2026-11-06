package domain

import (
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var (
	ErrInvalid        = fmt.Errorf("media: %w", apperr.ErrInvalidArgument)
	ErrUploadNotFound = fmt.Errorf("upload %w", apperr.ErrNotFound)
	ErrNotUploading   = fmt.Errorf("upload is not in progress: %w", apperr.ErrFailedPrecondition)
	ErrUploadGone     = fmt.Errorf("upload session is gone: %w", apperr.ErrFailedPrecondition)
	ErrSizeMismatch   = fmt.Errorf("uploaded size differs from the declared size: %w", apperr.ErrFailedPrecondition)
	ErrPartRejected   = fmt.Errorf("storage rejected a part ETag: %w", apperr.ErrFailedPrecondition)
	ErrDisabled       = fmt.Errorf("media is disabled: %w", apperr.ErrFailedPrecondition)
	ErrNotReady       = fmt.Errorf("media storage is not ready: %w", apperr.ErrUnavailable)
	ErrRetryLater     = fmt.Errorf("retry later: %w", apperr.ErrUnavailable)
)
