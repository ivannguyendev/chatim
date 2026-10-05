package mongostore

import (
	"errors"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

const namespaceExistsCode = 48

var ErrNotClustered = errors.New("mongostore: collection is not clustered on _id")

func onlyDuplicateKeys(err error) bool {
	bwe, ok := errors.AsType[mongo.BulkWriteException](err)
	if !ok || bwe.WriteConcernError != nil || len(bwe.WriteErrors) == 0 {
		return false
	}
	for _, we := range bwe.WriteErrors {
		if !mongo.IsDuplicateKeyError(we.WriteError) {
			return false
		}
	}
	return true
}

func namespaceExists(err error) bool {
	se, ok := errors.AsType[mongo.ServerError](err)
	return ok && se.HasErrorCode(namespaceExistsCode)
}

func batchIndexesWithin(errs []mongo.BulkWriteError, n int) bool {
	for _, we := range errs {
		if we.Index < 0 || we.Index >= n {
			return false
		}
	}
	return true
}
