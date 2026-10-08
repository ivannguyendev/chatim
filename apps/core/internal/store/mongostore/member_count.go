package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type memberCountDoc struct {
	Count int64 `bson:"member_count"`
	Ver   int64 `bson:"member_count_ver"`
}

func (s *Store) AddMemberCount(ctx context.Context, room uint64, delta int) (domain.MemberCount, error) {
	if err := store.ValidateMemberDelta(delta); err != nil {
		return domain.MemberCount{}, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return domain.MemberCount{}, fmt.Errorf("add %d to member count of room %d: %w", delta, room, domain.ErrRoomNotFound)
	}
	got, err := updateMemberCount(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, countIncrement(delta))
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = domain.ErrRoomNotFound
	}
	if err != nil {
		return domain.MemberCount{}, fmt.Errorf("add %d to member count of room %d: %w", delta, room, err)
	}
	return got, nil
}

func (s *Store) CountMembers(ctx context.Context, room uint64) (int, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return 0, err
	}
	n, err := s.committedMembers.CountDocuments(ctx, bson.D{{Key: "room_id", Value: key}, {Key: "state", Value: int64(domain.MemberActive)}})
	if err != nil {
		return 0, fmt.Errorf("count members of room %d: %w", room, err)
	}
	return int(n), nil
}

func (s *Store) SetMemberCount(ctx context.Context, room, base uint64, count int) (domain.MemberCount, bool, error) {
	if err := store.ValidateMemberCount(count); err != nil {
		return domain.MemberCount{}, false, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return domain.MemberCount{}, false, err
	}
	from, err := toInt64("member count ver", base)
	if err != nil {
		return domain.MemberCount{}, false, err
	}
	filter := bson.D{{Key: "_id", Value: key}, versionIs("member_count_ver", from)}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "member_count", Value: int64(count)}}},
		{Key: "$inc", Value: bson.D{{Key: "member_count_ver", Value: int64(1)}}},
	}
	got, err := updateMemberCount(ctx, s.rooms, filter, update)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return domain.MemberCount{}, false, nil
	case err != nil:
		return domain.MemberCount{}, false, fmt.Errorf("set member count of room %d at ver %d: %w", room, base, err)
	}
	return got, true, nil
}

func countIncrement(delta int) bson.D {
	return bson.D{{Key: "$inc", Value: memberCountFields(delta)}}
}

func memberCountFields(delta int) bson.D {
	return bson.D{{Key: "member_count", Value: int64(delta)}, {Key: "member_count_ver", Value: int64(1)}}
}

func updateMemberCount(ctx context.Context, rooms *mongo.Collection, filter, update bson.D) (domain.MemberCount, error) {
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After).
		SetProjection(bson.D{{Key: "member_count", Value: 1}, {Key: "member_count_ver", Value: 1}})
	var d memberCountDoc
	if err := rooms.FindOneAndUpdate(ctx, filter, update, opts).Decode(&d); err != nil {
		return domain.MemberCount{}, err
	}
	return decodeMemberCount(d)
}

func decodeMemberCount(d memberCountDoc) (domain.MemberCount, error) {
	ver, err := toUint64("member count ver", d.Ver)
	if err != nil {
		return domain.MemberCount{}, err
	}
	return domain.MemberCount{Count: int(d.Count), Ver: ver}, nil
}
