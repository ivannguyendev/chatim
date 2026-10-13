package memstore

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.RoomList = (*Rooms)(nil)

func (s *Rooms) CountRooms(ctx context.Context, tenant, user string, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := store.ValidateCountRooms(user, limit); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for k, m := range s.members {
		if k.user == user && m.Tenant == tenant && m.Active() {
			n++
		}
	}
	return min(n, limit), nil
}

func (s *Rooms) JoinedRoomOrder(ctx context.Context, q store.RoomListQuery) ([]store.RoomOrderRow, error) {
	return s.roomOrder(ctx, q, func(m domain.Member) domain.Member { return m })
}

func (s *Rooms) StepwiseRoomOrder(ctx context.Context, q store.RoomListQuery) ([]store.RoomOrderRow, error) {
	return s.roomOrder(ctx, q, indexedMember)
}

func (s *Rooms) roomOrder(ctx context.Context, q store.RoomListQuery, shape func(domain.Member) domain.Member) ([]store.RoomOrderRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []store.RoomOrderRow{}
	for k, m := range s.members {
		if k.user == q.User && m.Tenant == q.Tenant && m.Active() && q.Holds(m.Preferences) {
			rows = append(rows, store.RoomOrderRow{Member: shape(m), Activity: activityOf(k.room, s.rooms[k.room])})
		}
	}
	store.SortRoomOrder(rows)
	return rows, nil
}

func indexedMember(m domain.Member) domain.Member {
	return domain.Member{
		Room: m.Room, Tenant: m.Tenant, User: m.User, State: m.State, JoinedAt: m.JoinedAt,
		Preferences: domain.RoomPreferences{PinnedAt: m.Preferences.PinnedAt, ArchivedAt: m.Preferences.ArchivedAt},
	}
}

func activityOf(id uint64, r domain.Room) store.RoomActivity {
	return store.RoomActivity{Room: id, LastMessageAt: r.LastMsgAt, LastSeq: r.LastSeq, LastEditAt: r.LastEditAt}
}

func (s *Rooms) MembersByKeys(ctx context.Context, keys []store.MemberKey) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateMemberKeys(keys); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Member{}
	for _, k := range keys {
		if m, ok := s.members[memberKey{k.Room, k.User}]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Rooms) RoomActivities(ctx context.Context, rooms []uint64) ([]store.RoomActivity, error) {
	found, err := s.roomsByID(ctx, rooms)
	out := make([]store.RoomActivity, len(found))
	for i, r := range found {
		out[i] = activityOf(r.ID, r)
	}
	return out, err
}

func (s *Rooms) RoomInfos(ctx context.Context, rooms []uint64) ([]domain.Room, error) {
	return s.roomsByID(ctx, rooms)
}

func (s *Rooms) roomsByID(ctx context.Context, rooms []uint64) ([]domain.Room, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateRoomIDs(rooms); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Room{}
	for _, id := range rooms {
		if r, ok := s.rooms[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}
