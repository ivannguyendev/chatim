package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
	"github.com/ivannguyendev/chatim/pkg/mongobound"
)

var _ store.MessageVersions = (*Store)(nil)

type messageVersionDoc struct {
	ID      []byte `bson:"_id"`
	Version int32  `bson:"v"`
}

func (s *Store) MessageVersions(ctx context.Context, ks []store.MsgKey) (map[store.MsgKey]uint32, error) {
	if err := store.ValidateMessageLookup(ks); err != nil {
		return nil, err
	}
	out := make(map[store.MsgKey]uint32, len(ks))
	if len(ks) == 0 {
		return out, ctx.Err()
	}
	opts := options.Find().SetProjection(bson.D{{Key: "v", Value: 1}})
	cur, err := s.messages.Find(ctx, mongobound.IDs(msgIDs(ks)), opts)
	if err != nil {
		return nil, fmt.Errorf("versions of %d messages: %w", len(ks), err)
	}
	var docs []messageVersionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("versions of %d messages: %w", len(ks), err)
	}
	for _, d := range docs {
		room, thread, seq, err := keys.ParseMsg(d.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: message _id: %w", errCorrupt, err)
		}
		ver, err := toUint32("message version", d.Version)
		if err != nil {
			return nil, err
		}
		out[store.MsgKey{Room: room, Thread: thread, Seq: seq}] = ver
	}
	return out, nil
}
