package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	messagesFeedID          = "messages"
	changeStreamHistoryLost = 286
	feedMaxAwait            = time.Second
)

var _ store.ChangeFeed = (*Feed)(nil)

type Feed struct {
	messages *mongo.Collection
	state    *mongo.Collection
}

type feedPosition struct {
	Token bson.Raw       `bson:"token"`
	At    bson.Timestamp `bson:"at"`
}

type changeDoc struct {
	Token        bson.Raw       `bson:"_id"`
	ClusterTime  bson.Timestamp `bson:"clusterTime"`
	WallTime     time.Time      `bson:"wallTime"`
	FullDocument messageDoc     `bson:"fullDocument"`
}

func NewFeed(db *mongo.Database) *Feed {
	majority := options.Collection().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority())
	return &Feed{messages: db.Collection(messagesCollection, majority), state: db.Collection(reconcilerStateCollection, majority)}
}

func (f *Feed) Open(ctx context.Context) (store.Cursor, error) {
	opts := options.ChangeStream().SetMaxAwaitTime(feedMaxAwait)
	var saved feedPosition
	switch err := f.state.FindOne(ctx, bson.D{{Key: "_id", Value: messagesFeedID}}).Decode(&saved); {
	case err == nil:
		opts.SetStartAfter(saved.Token)
	case !errors.Is(err, mongo.ErrNoDocuments):
		return nil, fmt.Errorf("load change feed position: %w", err)
	}
	pipeline := mongo.Pipeline{{{Key: "$match", Value: bson.D{{Key: "operationType", Value: "insert"}}}}}
	cs, err := f.messages.Watch(ctx, pipeline, opts)
	if err != nil {
		return nil, feedError("open change stream", err)
	}
	return &feedCursor{cs: cs, state: f.state}, nil
}

func (f *Feed) Forget(ctx context.Context) error {
	if _, err := f.state.DeleteOne(ctx, bson.D{{Key: "_id", Value: messagesFeedID}}); err != nil {
		return fmt.Errorf("forget change feed position: %w", err)
	}
	return nil
}

type feedCursor struct {
	cs    *mongo.ChangeStream
	state *mongo.Collection
}

func (c *feedCursor) Next(ctx context.Context) (store.Change, error) {
	if !c.cs.Next(ctx) {
		if err := c.cs.Err(); err != nil {
			return store.Change{}, feedError("read change stream", err)
		}
		return store.Change{}, fmt.Errorf("read change stream: %w", apperr.ErrUnavailable)
	}
	var ev changeDoc
	if err := c.cs.Decode(&ev); err != nil {
		return store.Change{}, fmt.Errorf("%w: decode change event: %w", store.ErrCorruptChange, err)
	}
	m, err := decodeMessage(ev.FullDocument)
	if err != nil {
		return store.Change{}, fmt.Errorf("%w: %w", store.ErrCorruptChange, err)
	}
	pos, err := bson.Marshal(feedPosition{Token: ev.Token, At: ev.ClusterTime})
	if err != nil {
		return store.Change{}, fmt.Errorf("%w: encode position: %w", store.ErrCorruptChange, err)
	}
	return store.Change{Msg: m, CommittedAt: ev.WallTime, Position: pos}, nil
}

func (c *feedCursor) Confirm(ctx context.Context, pos store.Position) error {
	var p feedPosition
	if err := bson.Unmarshal(pos, &p); err != nil || len(p.Token) == 0 {
		return fmt.Errorf("%w: change feed position", apperr.ErrInvalidArgument)
	}
	filter := bson.D{{Key: "_id", Value: messagesFeedID}, {Key: "at", Value: bson.D{{Key: "$lt", Value: p.At}}}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "token", Value: p.Token}, {Key: "at", Value: p.At}}}}
	_, err := c.state.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("confirm change feed position: %w", err)
	}
	return nil
}

func (c *feedCursor) Close(ctx context.Context) error { return c.cs.Close(ctx) }

func feedError(op string, err error) error {
	if se, ok := errors.AsType[mongo.ServerError](err); ok && se.HasErrorCode(changeStreamHistoryLost) {
		return fmt.Errorf("%s: %w: %w", op, store.ErrFeedHistoryLost, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}
