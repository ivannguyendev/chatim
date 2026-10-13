package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Store) UpdateRoomInfo(ctx context.Context, room, base uint64, c store.RoomInfoChange) (bool, error) {
	if err := store.ValidateRoomInfoChange(room, c); err != nil {
		return false, err
	}
	id, err := toInt64("room id", room)
	if err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, base+1); err != nil {
		return false, err
	}
	ver, err := toInt64("info ver", base)
	if err != nil {
		return false, err
	}
	res, err := s.rooms.UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, infoVerIs(ver)}, roomInfoUpdate(c))
	if err != nil {
		return false, fmt.Errorf("update info of room %d at ver %d: %w", room, base, err)
	}
	return res.MatchedCount == 1, nil
}

func infoVerIs(base int64) bson.E {
	if base == 0 {
		return bson.E{Key: "info_ver", Value: bson.D{{Key: "$exists", Value: false}}}
	}
	return bson.E{Key: "info_ver", Value: base}
}

func roomInfoUpdate(c store.RoomInfoChange) bson.D {
	set := bson.D{{Key: "updated_at", Value: time.UnixMilli(c.At.UnixMilli()).UTC()}, {Key: "updated_by", Value: c.By}}
	if c.Update.Name != nil {
		set = append(set, bson.E{Key: "name", Value: *c.Update.Name})
	}
	update := bson.D{{Key: "$inc", Value: bson.D{{Key: "info_ver", Value: int64(1)}}}}
	switch a := c.Update.Avatar; {
	case a != nil && *a == "":
		update = append(update, bson.E{Key: "$unset", Value: bson.D{{Key: "avatar", Value: ""}}})
	case a != nil:
		set = append(set, bson.E{Key: "avatar", Value: *a})
	}
	return append(update, bson.E{Key: "$set", Value: set})
}
