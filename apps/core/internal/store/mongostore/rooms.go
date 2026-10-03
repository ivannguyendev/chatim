package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Store) Create(ctx context.Context, r domain.Room, members []domain.Member) error {
	if err := store.ValidateRoom(r, members); err != nil {
		return err
	}
	room, err := encodeRoom(r)
	if err != nil {
		return err
	}
	if _, err := s.rooms.InsertOne(ctx, room); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("create room %d: %w", r.ID, store.ErrRoomExists)
		}
		return fmt.Errorf("create room %d: %w", r.ID, err)
	}
	docs := make([]any, len(members))
	for i, m := range members {
		docs[i] = encodeMember(m, room.ID)
	}
	_, err = s.members.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err != nil && !onlyDuplicateKeys(err) {
		return fmt.Errorf("create room %d: insert members: %w", r.ID, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id uint64) (domain.Room, error) {
	key, err := toInt64("room id", id)
	if err != nil {
		return domain.Room{}, fmt.Errorf("get room %d: %w", id, domain.ErrRoomNotFound)
	}
	var d roomDoc
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound); err != nil {
		return domain.Room{}, fmt.Errorf("get room %d: %w", id, err)
	}
	return decodeRoom(d)
}

func (s *Store) Member(ctx context.Context, room uint64, user string) (domain.Member, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return domain.Member{}, fmt.Errorf("member %q of room %d: %w", user, room, domain.ErrNotMember)
	}
	var d memberDoc
	filter := bson.D{{Key: "r", Value: key}, {Key: "u", Value: user}}
	if err := findOne(ctx, s.members, filter, &d, domain.ErrNotMember); err != nil {
		return domain.Member{}, fmt.Errorf("member %q of room %d: %w", user, room, err)
	}
	return decodeMember(d)
}

func findOne(ctx context.Context, coll *mongo.Collection, filter bson.D, out any, missing error) error {
	err := coll.FindOne(ctx, filter).Decode(out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return missing
	}
	return err
}
