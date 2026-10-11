package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.SharedMentions = (*Store)(nil)

type sharedMentionDoc struct {
	ID int64     `bson:"_id"`
	At time.Time `bson:"last_shared_mention_at"`
}

func (s *Store) RaiseSharedMention(ctx context.Context, room uint64, at time.Time) error {
	if err := store.ValidateSharedMention(room, at); err != nil {
		return err
	}
	id, err := toInt64("room id", room)
	if err != nil {
		return err
	}
	update := bson.D{{Key: "$max", Value: bson.D{{Key: "last_shared_mention_at", Value: at.UTC()}}}}
	res, err := s.rooms.UpdateOne(ctx, bson.D{{Key: "_id", Value: id}}, update)
	switch {
	case err != nil:
		return fmt.Errorf("raise shared mention of room %d: %w", room, err)
	case res.MatchedCount == 0:
		return domain.ErrRoomNotFound
	}
	return nil
}

func (s *Store) SharedMentionTimes(ctx context.Context, rooms []uint64) (map[uint64]time.Time, error) {
	if err := store.ValidateSharedMentionRooms(rooms); err != nil {
		return nil, err
	}
	out := make(map[uint64]time.Time)
	for start := 0; start < len(rooms); start += store.SharedMentionChunk {
		if err := s.sharedMentionChunk(ctx, rooms[start:min(len(rooms), start+store.SharedMentionChunk)], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) sharedMentionChunk(ctx context.Context, rooms []uint64, out map[uint64]time.Time) error {
	ids := make(bson.A, len(rooms))
	for i, r := range rooms {
		id, err := toInt64("room id", r)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}, {Key: "last_shared_mention_at", Value: bson.D{{Key: "$exists", Value: true}}}}
	opts := options.Find().SetProjection(bson.D{{Key: "last_shared_mention_at", Value: 1}})
	cur, err := s.rooms.Find(ctx, filter, opts)
	if err != nil {
		return fmt.Errorf("shared mention times of %d rooms: %w", len(rooms), err)
	}
	var docs []sharedMentionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return fmt.Errorf("shared mention times of %d rooms: %w", len(rooms), err)
	}
	for _, d := range docs {
		room, err := toUint64("room id", d.ID)
		if err != nil {
			return err
		}
		out[room] = d.At
	}
	return nil
}
