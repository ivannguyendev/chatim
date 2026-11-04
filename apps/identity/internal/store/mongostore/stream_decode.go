package mongostore

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/core/redact"
)

const (
	historyLostCode = 286
	streamFatalCode = 280
)

var streamedCollections = []string{
	usersCollection, tenantsCollection, trustedIssuersCollection, trustedKeysCollection, revokedSessionsCollection,
}

var positionLostOperations = []string{"invalidate", "drop", "dropDatabase", "rename"}

var kindsByCollection = map[string][]store.EventKind{
	usersCollection:           {store.EventUser},
	tenantsCollection:         {store.EventTrust, store.EventRevocation},
	trustedIssuersCollection:  {store.EventTrust},
	trustedKeysCollection:     {store.EventTrust},
	revokedSessionsCollection: {store.EventRevocation},
}

type streamDoc struct {
	OperationType string `bson:"operationType"`
	Namespace     struct {
		Collection string `bson:"coll"`
	} `bson:"ns"`
	FullDocument *directoryDoc `bson:"fullDocument"`
}

func decodeStreamEvent(raw bson.Raw) (store.StreamEvent, error) {
	var doc streamDoc
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return store.StreamEvent{}, fmt.Errorf("%w: decode change event: %w", apperr.ErrUnavailable, err)
	}
	for _, op := range positionLostOperations {
		if doc.OperationType == op {
			return store.StreamEvent{}, fmt.Errorf("%w: %s on %s", store.ErrStreamLost, op, doc.Namespace.Collection)
		}
	}
	kinds := kindsByCollection[doc.Namespace.Collection]
	if doc.Namespace.Collection != usersCollection {
		return store.StreamEvent{Kinds: kinds}, nil
	}
	if doc.FullDocument == nil {
		return store.StreamEvent{}, nil
	}
	return store.StreamEvent{Kinds: kinds, User: doc.FullDocument.entry()}, nil
}

func (s *Store) streamErr(err error) error {
	if hasCode(err, historyLostCode) || hasCode(err, streamFatalCode) {
		return fmt.Errorf("%w: %w", store.ErrStreamLost, redact.Error(err, s.uri))
	}
	return s.mapErr(err)
}
