package mongostore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	streamAwait        = time.Second
	streamCloseTimeout = 2 * time.Second
)

var (
	writeOperations   = []string{"insert", "update", "replace"}
	versionedOnUpdate = []string{usersCollection, tenantsCollection}
)

type changeStream struct {
	s      *Store
	cursor *mongo.ChangeStream
}

func streamPipeline() mongo.Pipeline {
	ownWrite := bson.D{
		{Key: "ns.coll", Value: bson.D{{Key: "$in", Value: streamedCollections}}},
		{Key: "operationType", Value: bson.D{{Key: "$in", Value: writeOperations}}},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "operationType", Value: bson.D{{Key: "$ne", Value: "update"}}}},
			bson.D{{Key: "ns.coll", Value: bson.D{{Key: "$nin", Value: versionedOnUpdate}}}},
			bson.D{{Key: "updateDescription.updatedFields.ver", Value: bson.D{{Key: "$exists", Value: true}}}},
		}},
	}
	positionLost := bson.D{{Key: "operationType", Value: bson.D{{Key: "$in", Value: positionLostOperations}}}}
	project := bson.D{{Key: "_id", Value: 1}, {Key: "operationType", Value: 1}, {Key: "ns", Value: 1}}
	for _, f := range directoryFields {
		project = append(project, bson.E{Key: "fullDocument." + f, Value: 1})
	}
	return mongo.Pipeline{
		{{Key: "$match", Value: bson.D{{Key: "$or", Value: bson.A{ownWrite, positionLost}}}}},
		{{Key: "$project", Value: project}},
	}
}

func (s *Store) StreamStart(ctx context.Context) (store.StreamToken, error) {
	cursor, err := s.db.Watch(ctx, streamPipeline())
	if err != nil {
		return nil, s.streamErr(err)
	}
	defer func() { _ = closeCursor(ctx, cursor) }()
	token := cursor.ResumeToken()
	if token == nil {
		return nil, fmt.Errorf("%w: change stream gave no start token", apperr.ErrUnavailable)
	}
	return store.StreamToken(slices.Clone(token)), nil
}

func (s *Store) OpenStream(ctx context.Context, from store.StreamToken) (store.ChangeStream, error) {
	if err := bson.Raw(from).Validate(); len(from) == 0 || err != nil {
		return nil, fmt.Errorf("%w: stream token is not a document", apperr.ErrInvalidArgument)
	}
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup).SetMaxAwaitTime(streamAwait).
		SetResumeAfter(bson.Raw(from))
	cursor, err := s.db.Watch(ctx, streamPipeline(), opts)
	if err != nil {
		return nil, s.streamErr(err)
	}
	return &changeStream{s: s, cursor: cursor}, nil
}

func (c *changeStream) Next(ctx context.Context) (store.StreamEvent, bool, error) {
	for c.cursor.TryNext(ctx) {
		ev, err := decodeStreamEvent(c.cursor.Current)
		if err != nil {
			return store.StreamEvent{}, false, err
		}
		if len(ev.Kinds) == 0 {
			continue
		}
		ev.Token = c.token()
		ev.CaughtUp = c.cursor.RemainingBatchLength() == 0
		return ev, true, nil
	}
	if err := c.cursor.Err(); err != nil {
		return store.StreamEvent{}, false, c.s.streamErr(err)
	}
	if c.cursor.ID() == 0 {
		return store.StreamEvent{}, false, fmt.Errorf("%w: change stream cursor closed", apperr.ErrUnavailable)
	}
	return store.StreamEvent{Token: c.token()}, false, nil
}

func (c *changeStream) Close(ctx context.Context) error {
	return c.s.mapErr(closeCursor(ctx, c.cursor))
}

func (c *changeStream) token() store.StreamToken {
	return store.StreamToken(slices.Clone(c.cursor.ResumeToken()))
}

func closeCursor(ctx context.Context, cursor *mongo.ChangeStream) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), streamCloseTimeout)
	defer cancel()
	return cursor.Close(ctx)
}
