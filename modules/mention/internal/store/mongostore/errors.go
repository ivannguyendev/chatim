package mongostore

import (
	"errors"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	namespaceExistsCode = 48
	indexNotFoundCode   = 27
)

var (
	ErrNotClustered = errors.New("mongostore: collection is not clustered on _id")
	errCorrupt      = errors.New("mongostore: corrupt document")
)

func hasCode(err error, code int) bool {
	se, ok := errors.AsType[mongo.ServerError](err)
	return ok && se.HasErrorCode(code)
}

func toInt64(field string, v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("%w: %s", apperr.ErrInvalidArgument, field)
	}
	return int64(v), nil
}

func toUint64(field string, v int64) (uint64, error) {
	if v < 0 {
		return 0, fmt.Errorf("%w: %s %d", errCorrupt, field, v)
	}
	return uint64(v), nil
}

func literal(v any) bson.D { return bson.D{{Key: "$literal", Value: v}} }

func narrowUint32(field string, v int64) (uint32, error) {
	if v < 0 || v > math.MaxUint32 {
		return 0, fmt.Errorf("%w: %s %d out of range", errCorrupt, field, v)
	}
	return uint32(v), nil
}
