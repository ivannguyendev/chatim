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

type memberOrderDoc struct {
	Room       int64     `bson:"room_id"`
	JoinedAt   time.Time `bson:"joined_at"`
	PinnedAt   time.Time `bson:"pinned_at"`
	ArchivedAt time.Time `bson:"archived_at"`
}

var memberOrderFields = bson.D{{Key: "room_id", Value: 1}, {Key: "joined_at", Value: 1}, {Key: "pinned_at", Value: 1}, {Key: "archived_at", Value: 1}, {Key: "_id", Value: 0}}

func (s *Store) StepwiseRoomOrder(ctx context.Context, q store.RoomListQuery) ([]store.RoomOrderRow, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	members, err := s.orderedMembers(ctx, q)
	if err != nil {
		return nil, err
	}
	rooms := make([]uint64, len(members))
	for i, m := range members {
		rooms[i] = m.Room
	}
	acts := make(map[uint64]store.RoomActivity, len(rooms))
	for from := 0; from < len(rooms); from += store.RoomActivityBatch {
		found, err := s.RoomActivities(ctx, rooms[from:min(from+store.RoomActivityBatch, len(rooms))])
		if err != nil {
			return nil, err
		}
		for _, a := range found {
			acts[a.Room] = a
		}
	}
	rows := make([]store.RoomOrderRow, len(members))
	for i, m := range members {
		act, ok := acts[m.Room]
		if !ok {
			act.Room = m.Room
		}
		rows[i] = store.RoomOrderRow{Member: m, Activity: act}
	}
	store.SortRoomOrder(rows)
	return rows, nil
}

func (s *Store) orderedMembers(ctx context.Context, q store.RoomListQuery) ([]domain.Member, error) {
	cur, err := s.members.Find(ctx, memberRoomsFilter(q.Tenant, q.User), options.Find().SetProjection(memberOrderFields))
	if err != nil {
		return nil, fmt.Errorf("room order members of %q: %w", q.User, err)
	}
	var docs []memberOrderDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("room order members of %q: %w", q.User, err)
	}
	out := make([]domain.Member, 0, len(docs))
	for _, d := range docs {
		room, err := toUint64("member room id", d.Room)
		if err != nil {
			return nil, err
		}
		prefs := domain.RoomPreferences{PinnedAt: d.PinnedAt, ArchivedAt: d.ArchivedAt}
		if q.Holds(prefs) {
			out = append(out, domain.Member{Room: room, Tenant: q.Tenant, User: q.User, State: domain.MemberActive, JoinedAt: d.JoinedAt, Preferences: prefs})
		}
	}
	return out, nil
}
