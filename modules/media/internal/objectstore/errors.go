package objectstore

import (
	"errors"
	"fmt"

	"github.com/minio/minio-go/v7"
)

var (
	ErrNoSuchUpload  = errors.New("objectstore: no such multipart upload")
	ErrInvalidPart   = errors.New("objectstore: part list rejected")
	ErrNotFound      = errors.New("objectstore: object not found")
	ErrBucketMissing = errors.New("objectstore: bucket does not exist")
	ErrAccess        = errors.New("objectstore: access denied")
	ErrUnavailable   = errors.New("objectstore: storage unavailable")
)

func classify(err error) error {
	if err == nil {
		return nil
	}
	var kind error
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchUpload":
		kind = ErrNoSuchUpload
	case "InvalidPart", "InvalidPartOrder", "EntityTooSmall":
		kind = ErrInvalidPart
	case "NoSuchKey", "NotFound":
		kind = ErrNotFound
	case "NoSuchBucket":
		kind = ErrBucketMissing
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "Forbidden":
		kind = ErrAccess
	default:
		kind = ErrUnavailable
	}
	return fmt.Errorf("%w: %w", kind, err)
}

func resultOf(err error) result {
	switch {
	case err == nil:
		return resultOK
	case errors.Is(err, ErrNoSuchUpload), errors.Is(err, ErrInvalidPart), errors.Is(err, ErrNotFound):
		return resultClient
	}
	return resultError
}
