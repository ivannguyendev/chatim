package mongostore

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var _ store.RoomTails = (*Store)(nil)

type tailDoc struct {
	ID         []byte    `bson:"_id"`
	From       string    `bson:"f"`
	Deleted    bool      `bson:"d,omitempty"`
	SkipUnread bool      `bson:"nu,omitempty"`
	CreatedAt  time.Time `bson:"ts"`
}

var tailFields = bson.D{{Key: "f", Value: 1}, {Key: "d", Value: 1}, {Key: "nu", Value: 1}, {Key: "ts", Value: 1}}

func (s *Store) TailWindow(ctx context.Context, room, afterSeq uint64, limit int) ([]domain.TailMessage, error) {
	if err := store.ValidateTailWindow(room, afterSeq, limit); err != nil {
		return nil, err
	}
	q := tailRange(room, afterSeq, limit)
	opts := options.Find().SetSort(q.sort()).SetLimit(q.limit).SetProjection(tailFields)
	cur, err := s.messages.Find(ctx, q.filter, opts)
	if err != nil {
		return nil, fmt.Errorf("tail of room %d after %d: %w", room, afterSeq, err)
	}
	var docs []tailDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("tail of room %d after %d: %w", room, afterSeq, err)
	}
	out := make([]domain.TailMessage, len(docs))
	for i, d := range docs {
		_, _, seq, err := keys.ParseMsg(d.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: message _id: %w", errCorrupt, err)
		}
		out[i] = domain.TailMessage{Seq: seq, From: d.From, Deleted: d.Deleted, SkipUnread: d.SkipUnread, CreatedAt: d.CreatedAt}
	}
	return out, nil
}

func tailRange(room, afterSeq uint64, limit int) messageQuery {
	lo, hi := keys.MsgRange(room, 0, afterSeq, math.MaxUint64)
	return messageQuery{
		filter: bson.D{{Key: "_id", Value: bson.D{{Key: "$gt", Value: lo}, {Key: "$lt", Value: hi}}}},
		desc:   true,
		limit:  int64(limit),
	}
}

func (s *Store) TailPreviews(ctx context.Context, ks []store.MsgKey) ([]domain.Message, error) {
	if err := store.ValidateTailPreviews(ks); err != nil {
		return nil, err
	}
	if len(ks) == 0 {
		return []domain.Message{}, ctx.Err()
	}
	ids := make(bson.A, len(ks))
	for i, k := range ks {
		ids[i] = msgID(k)
	}
	msgs, err := find(ctx, s.messages, messageQuery{filter: bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}})
	if err != nil {
		return nil, fmt.Errorf("tail previews of %d messages: %w", len(ks), err)
	}
	store.SortByKey(msgs)
	return msgs, nil
}
