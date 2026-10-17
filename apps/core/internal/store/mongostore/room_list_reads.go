package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
	"github.com/ivannguyendev/chatim/pkg/mongobound"
)

var roomInfoFields = bson.D{{Key: "pins", Value: 0}, {Key: "pin_ver", Value: 0}}

func (s *Store) MembersByKeys(ctx context.Context, ks []store.MemberKey) ([]domain.Member, error) {
	if err := store.ValidateMemberKeys(ks); err != nil {
		return nil, err
	}
	if len(ks) == 0 {
		return []domain.Member{}, ctx.Err()
	}
	ids := make([][]byte, len(ks))
	for i, k := range ks {
		ids[i] = keys.Member(k.Room, k.User)
	}
	found, err := findMembers(ctx, s.members, mongobound.IDs(ids))
	if err != nil {
		return nil, fmt.Errorf("members by %d keys: %w", len(ks), err)
	}
	byKey := make(map[store.MemberKey]domain.Member, len(found))
	for _, m := range found {
		byKey[store.MemberKey{Room: m.Room, User: m.User}] = m
	}
	out := make([]domain.Member, 0, len(found))
	for _, k := range ks {
		if m, ok := byKey[k]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Store) RoomActivities(ctx context.Context, rooms []uint64) ([]store.RoomActivity, error) {
	var docs []roomActivityDoc
	if err := s.roomsByID(ctx, rooms, roomActivityFields, &docs); err != nil {
		return nil, fmt.Errorf("activities of %d rooms: %w", len(rooms), err)
	}
	byID := make(map[uint64]store.RoomActivity, len(docs))
	for _, d := range docs {
		a, err := decodeRoomActivity(d)
		if err != nil {
			return nil, err
		}
		byID[a.Room] = a
	}
	return inRoomOrder(rooms, byID), nil
}

func (s *Store) RoomInfos(ctx context.Context, rooms []uint64) ([]domain.Room, error) {
	var docs []roomDoc
	if err := s.roomsByID(ctx, rooms, roomInfoFields, &docs); err != nil {
		return nil, fmt.Errorf("infos of %d rooms: %w", len(rooms), err)
	}
	byID := make(map[uint64]domain.Room, len(docs))
	for _, d := range docs {
		r, err := decodeRoom(d)
		if err != nil {
			return nil, err
		}
		byID[r.ID] = r
	}
	return inRoomOrder(rooms, byID), nil
}

func inRoomOrder[T any](rooms []uint64, byID map[uint64]T) []T {
	out := make([]T, 0, len(byID))
	for _, id := range rooms {
		if v, ok := byID[id]; ok {
			out = append(out, v)
		}
	}
	return out
}

func (s *Store) roomsByID(ctx context.Context, rooms []uint64, fields bson.D, out any) error {
	if err := store.ValidateRoomIDs(rooms); err != nil {
		return err
	}
	if len(rooms) == 0 {
		return ctx.Err()
	}
	ids := make(bson.A, len(rooms))
	for i, r := range rooms {
		id, err := toInt64("room id", r)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	cur, err := s.rooms.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, options.Find().SetProjection(fields))
	if err != nil {
		return err
	}
	return cur.All(ctx, out)
}
