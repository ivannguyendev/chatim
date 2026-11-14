package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
)

func (s *Store) MemberSample(ctx context.Context, room uint64, limit int) ([]string, error) {
	if err := store.ValidateMemberSample(room, limit); err != nil {
		return nil, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	opts := options.Find().SetLimit(int64(limit)).SetProjection(memberIDFields).SetHint(membersRoomKeys)
	cur, err := s.members.Find(ctx, memberIDsFilter(key), opts)
	if err != nil {
		return nil, fmt.Errorf("member sample of room %d: %w", room, err)
	}
	var docs []memberIDDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("member sample of room %d: %w", room, err)
	}
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.User
	}
	return out, nil
}

func avatarVerFilter(room int64, ver uint64) (bson.D, error) {
	if ver == 0 {
		return bson.D{{Key: "_id", Value: room}, {Key: "avatar_ver", Value: bson.D{{Key: "$exists", Value: false}}}}, nil
	}
	v, err := toInt64("avatar ver", ver)
	if err != nil {
		return nil, err
	}
	return bson.D{{Key: "_id", Value: room}, {Key: "avatar_ver", Value: v}}, nil
}

func (s *Store) SetAvatarUsers(ctx context.Context, room, ver uint64, next []string) error {
	if err := store.ValidateAvatarUsers(room, ver, next); err != nil {
		return err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return err
	}
	filter, err := avatarVerFilter(key, ver)
	if err != nil {
		return err
	}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "avatar_users", Value: append([]string{}, next...)}}},
		{Key: "$inc", Value: bson.D{{Key: "avatar_ver", Value: int64(1)}}},
	}
	res, err := s.rooms.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("set avatar users of room %d: %w", room, err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrRetryLater
	}
	return nil
}
