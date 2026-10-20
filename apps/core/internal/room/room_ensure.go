package room

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errRoomIDTaken = fmt.Errorf("room id taken by another room: %w", apperr.ErrUnavailable)

type founding struct {
	room    domain.Room
	members []domain.Member
	by      string
	at      time.Time
	resume  bool
	claimed func(domain.Room)
}

func (f founding) join(room uint64) (domain.Join, []string) {
	j := domain.Join{Room: room, Tenant: f.room.Tenant, RequestID: domain.CreationRequestID(room), By: f.by, At: f.at}
	users := make([]string, len(f.members))
	for i, m := range f.members {
		users[i] = m.User
		if m.Role == domain.RoleOwner {
			j.Owner = m.User
		}
	}
	return j, users
}

func (s *Service) ensureRoom(ctx context.Context, f founding) (domain.Room, bool, error) {
	armed, err := s.d.Members.ArmCount(ctx, f.room.ID)
	if err != nil {
		return domain.Room{}, false, err
	}
	room, inserted, err := s.insertRoom(ctx, f)
	if err != nil {
		if errors.Is(err, errRoomIDTaken) {
			settle, done := member.Settling(ctx)
			defer done()
			s.d.Members.SettleCount(settle, f.room.ID, armed, 0)
		}
		return domain.Room{}, false, err
	}
	if f.claimed != nil {
		f.claimed(room)
	}
	j, users := f.join(room.ID)
	res, err := s.d.Members.AddFounders(ctx, j, users)
	if err != nil {
		return domain.Room{}, false, err
	}
	settle, done := member.Settling(ctx)
	defer done()
	count, counted := s.d.Members.SettleCount(settle, room.ID, armed, res.Changed)
	room = s.settledRoom(settle, room, count, counted)
	created := inserted || res.Changed > 0
	if created {
		_ = s.d.Events.Enqueue(room.ID, foundingEvents(room, res.Members, count, counted, f))
	}
	return room, created, nil
}

func (s *Service) settledRoom(ctx context.Context, room domain.Room, count domain.MemberCount, counted bool) domain.Room {
	if counted {
		room.MemberCount, room.MemberCountVer = count.Count, count.Ver
		return room
	}
	stored, err := s.d.Rooms.Get(ctx, room.ID)
	if err != nil {
		s.d.Log.WarnContext(ctx, "new room not read back; replying with the count seen before", "room", room.ID, "err", err)
		return room
	}
	return stored
}

func (s *Service) insertRoom(ctx context.Context, f founding) (domain.Room, bool, error) {
	r := f.room
	err := s.d.Rooms.InsertRoom(ctx, r)
	switch {
	case err == nil:
		r.MemberCount, r.MemberCountVer, r.InfoVer = 0, 0, 1
		return r, true, nil
	case !errors.Is(err, store.ErrRoomExists):
		return domain.Room{}, false, err
	}
	stored, err := s.d.Rooms.Get(ctx, r.ID)
	if err != nil {
		return domain.Room{}, false, err
	}
	if !sameRoom(f, stored) {
		return domain.Room{}, false, errRoomIDTaken
	}
	return stored, false, nil
}

func sameRoom(f founding, stored domain.Room) bool {
	r := f.room
	switch {
	case stored.Tenant != r.Tenant || stored.Type != r.Type:
		return false
	case r.Type == domain.RoomDM:
		return stored.DMKey == r.DMKey
	default:
		return f.resume && stored.CreatedBy == r.CreatedBy
	}
}

func foundingEvents(room domain.Room, members []domain.Member, count domain.MemberCount, counted bool, f founding) []*chatimv1.Event {
	events := make([]*chatimv1.Event, 0, len(members)+2)
	events = append(events, pbconv.RoomCreated(room))
	for _, m := range members {
		if ev := pbconv.MemberEvent(room.Type, m); ev != nil {
			events = append(events, ev)
		}
	}
	if counted {
		events = append(events, pbconv.MemberCountChanged(room, count, f.by, f.at))
	}
	return events
}
