package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type roomActivityDoc struct {
	ID            int64     `bson:"_id"`
	LastMessageAt time.Time `bson:"last_message_at,omitempty"`
	LastSeq       int64     `bson:"last_seq,omitempty"`
	LastEditAt    time.Time `bson:"last_edit_at,omitempty"`
}

type joinedRowDoc struct {
	Member   memberDoc       `bson:",inline"`
	Activity roomActivityDoc `bson:"activity"`
}

var roomActivityFields = bson.D{{Key: "last_message_at", Value: 1}, {Key: "last_seq", Value: 1}, {Key: "last_edit_at", Value: 1}}

func (s *Store) JoinedRoomOrder(ctx context.Context, q store.RoomListQuery) ([]store.RoomOrderRow, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	cur, err := s.members.Aggregate(ctx, joinedRoomPipeline(q))
	if err != nil {
		return nil, fmt.Errorf("joined room order of %q: %w", q.User, err)
	}
	var docs []joinedRowDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("joined room order of %q: %w", q.User, err)
	}
	out := make([]store.RoomOrderRow, len(docs))
	for i, d := range docs {
		m, err := decodeMember(d.Member)
		if err != nil {
			return nil, err
		}
		d.Activity.ID = d.Member.Room
		act, err := decodeRoomActivity(d.Activity)
		if err != nil {
			return nil, err
		}
		out[i] = store.RoomOrderRow{Member: m, Activity: act}
	}
	return out, nil
}

func joinedRoomPipeline(q store.RoomListQuery) bson.A {
	lookup := bson.D{
		{Key: "from", Value: roomsCollection}, {Key: "localField", Value: "room_id"}, {Key: "foreignField", Value: "_id"},
		{Key: "pipeline", Value: bson.A{bson.D{{Key: "$project", Value: roomActivityFields}}}}, {Key: "as", Value: "activity"},
	}
	sortAt := bson.D{{Key: "$max", Value: bson.A{"$joined_at", "$activity.last_message_at"}}}
	return bson.A{
		bson.D{{Key: "$match", Value: memberRoomsFilter(q.Tenant, q.User)}},
		bson.D{{Key: "$lookup", Value: lookup}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "activity", Value: bson.D{{Key: "$first", Value: "$activity"}}}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "sort_at", Value: sortAt}}}},
		bson.D{{Key: "$match", Value: folderFilter(q.Folder)}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "pinned_at", Value: -1}, {Key: "sort_at", Value: -1}, {Key: "room_id", Value: -1}}}},
	}
}

func folderFilter(f domain.RoomFolder) bson.D {
	return bson.D{{Key: "archived_at", Value: bson.D{{Key: "$exists", Value: f == domain.FolderArchived}}}}
}

func decodeRoomActivity(d roomActivityDoc) (store.RoomActivity, error) {
	room, err := toUint64("room id", d.ID)
	if err != nil {
		return store.RoomActivity{}, err
	}
	seq, err := toUint64("room last seq", d.LastSeq)
	if err != nil {
		return store.RoomActivity{}, err
	}
	return store.RoomActivity{Room: room, LastMessageAt: d.LastMessageAt, LastSeq: seq, LastEditAt: d.LastEditAt}, nil
}
