package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (m *Mentions) MarkRead(ctx context.Context, room uint64, user string, readSeq uint64) error {
	if err := store.ValidateMentionRead(room, user, readSeq); err != nil {
		return err
	}
	at, err := placeOf(store.MsgKey{Room: room, Seq: readSeq})
	if err != nil {
		return err
	}
	moves := []struct {
		seq      string
		from, to int64
	}{
		{"$lte", mentionLive, mentionRead},
		{"$gt", mentionRead, mentionLive},
	}
	target := domain.MentionTarget{Kind: domain.MentionUser, ID: user}.Name(room)
	for _, mv := range moves {
		filter := bson.D{
			{Key: "room_id", Value: at.room}, {Key: "target", Value: target}, {Key: "thread_root", Value: int64(0)},
			{Key: "seq", Value: bson.D{{Key: mv.seq, Value: at.seq}}}, {Key: "state", Value: mv.from},
		}
		update := bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: mv.to}}}}
		if _, err := m.coll.UpdateMany(ctx, filter, update); err != nil {
			return fmt.Errorf("mark mentions of %q in room %d read up to %d: %w", user, room, readSeq, err)
		}
	}
	return nil
}
