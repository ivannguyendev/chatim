package mongostore

import (
	"context"
	"fmt"
	"math"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type messageQuery struct {
	filter bson.D
	desc   bool
	limit  int64
}

func (q messageQuery) sort() bson.D {
	dir := 1
	if q.desc {
		dir = -1
	}
	return bson.D{{Key: "_id", Value: dir}}
}

func (s *Store) Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	mq := pageRange(q)
	msgs, err := find(ctx, s.messages, mq)
	if err != nil {
		return nil, fmt.Errorf("page %d/%d: %w", q.Room, q.Thread, err)
	}
	if mq.desc {
		slices.Reverse(msgs)
	}
	return msgs, nil
}

func pageRange(q store.PageQuery) messageQuery {
	from, to, lower, desc := uint64(0), uint64(math.MaxUint64), "$gte", true
	switch q.Anchor {
	case store.Latest:
	case store.Oldest:
		desc = false
	case store.Before:
		to = q.Seq
	case store.After:
		from, lower, desc = q.Seq, "$gt", false
	}
	lo, hi := keys.MsgRange(q.Room, q.Thread, from, to)
	return messageQuery{
		filter: bson.D{{Key: "_id", Value: bson.D{{Key: lower, Value: lo}, {Key: "$lt", Value: hi}}}},
		desc:   desc,
		limit:  int64(q.Limit),
	}
}

func find(ctx context.Context, coll *mongo.Collection, q messageQuery) ([]domain.Message, error) {
	opts := options.Find().SetSort(q.sort())
	if q.limit > 0 {
		opts.SetLimit(q.limit)
	}
	cur, err := coll.Find(ctx, q.filter, opts)
	if err != nil {
		return nil, err
	}
	var docs []messageDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return decodeMessages(docs)
}
