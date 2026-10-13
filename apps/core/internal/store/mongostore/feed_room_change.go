package mongostore

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const roomInfoVerField = "info_ver"

func decodeRoomChange(ev changeDoc) (store.Change, error) {
	if ev.OperationType == "update" {
		return roomFromUpdate(ev)
	}
	var d roomDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: room document: %w", errCorrupt, err)
	}
	r, err := decodeRoom(d)
	if err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.RoomInserted, Room: r, CommittedAt: ev.WallTime}, nil
}

func roomFromUpdate(ev changeDoc) (store.Change, error) {
	raw, ok := ev.DocumentKey.ID.AsInt64OK()
	if !ok || raw <= 0 {
		return store.Change{}, fmt.Errorf("%w: room update without a positive numeric _id", errCorrupt)
	}
	fields := ev.UpdateDescription.UpdatedFields
	ver, ok := fields.Lookup(roomInfoVerField).AsInt64OK()
	if !ok || ver <= 0 {
		return store.Change{}, fmt.Errorf("%w: room update without a positive info_ver", errCorrupt)
	}
	room := domain.Room{ID: uint64(raw), InfoVer: uint64(ver)}
	return store.Change{Kind: store.RoomChanged, Room: room, CommittedAt: ev.WallTime}, nil
}

func roomFeedBranch() bson.D {
	return bson.D{
		{Key: "operationType", Value: "update"},
		{Key: "ns.coll", Value: roomsCollection},
		{Key: updatedFieldsPath + roomInfoVerField, Value: bson.D{{Key: "$exists", Value: true}}},
	}
}
