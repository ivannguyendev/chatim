package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var _ store.MessageTimes = (*Store)(nil)

type sentTimeDoc struct {
	ID []byte    `bson:"_id"`
	At time.Time `bson:"ts"`
}

func (s *Store) SentTimes(ctx context.Context, ks []store.MsgKey) (map[store.MsgKey]time.Time, error) {
	if err := store.ValidateSentTimeKeys(ks); err != nil {
		return nil, err
	}
	out := make(map[store.MsgKey]time.Time, len(ks))
	if len(ks) == 0 {
		return out, ctx.Err()
	}
	ids := make(bson.A, len(ks))
	for i, k := range ks {
		ids[i] = msgID(k)
	}
	opts := options.Find().SetProjection(bson.D{{Key: "ts", Value: 1}})
	cur, err := s.messages.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, opts)
	if err != nil {
		return nil, fmt.Errorf("sent times of %d messages: %w", len(ks), err)
	}
	var docs []sentTimeDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("sent times of %d messages: %w", len(ks), err)
	}
	for _, d := range docs {
		room, thread, seq, err := keys.ParseMsg(d.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: message _id: %w", errCorrupt, err)
		}
		out[store.MsgKey{Room: room, Thread: thread, Seq: seq}] = d.At.UTC()
	}
	return out, nil
}
