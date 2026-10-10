package grpcsrv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const settleTimeout = 2 * time.Second

var errRoomIDTaken = fmt.Errorf("room id taken by another room: %w", apperr.ErrUnavailable)

type RoomMembers interface {
	AddMembers(ctx context.Context, j domain.Join, users []string) (store.JoinResult, error)
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	AddMemberCount(ctx context.Context, room uint64, delta int) (domain.MemberCount, error)
}

type founding struct {
	room    domain.Room
	members []domain.Member
	by      string
	at      time.Time
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

func settling(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
}

func (s *Service) ensureRoom(ctx context.Context, f founding) (domain.Room, bool, error) {
	timer, err := s.timers.ArmMemberCountCheck(ctx, f.room.ID)
	if err != nil {
		s.log.WarnContext(ctx, "member count check timer not armed; room not created", "room", f.room.ID, "err", err)
		return domain.Room{}, false, domain.ErrRetryLater
	}
	room, inserted, err := s.insertRoom(ctx, f.room)
	if err != nil {
		if errors.Is(err, errRoomIDTaken) {
			s.disarm(ctx, timer)
		}
		return domain.Room{}, false, err
	}
	j, users := f.join(room.ID)
	res, err := s.members.AddMembers(ctx, j, users)
	if err != nil {
		return domain.Room{}, false, err
	}
	settle, done := settling(ctx)
	defer done()
	count, counted := s.settleCount(settle, room.ID, timer, res.Changed)
	if counted {
		room.MemberCount, room.MemberCountVer = count.Count, count.Ver
	}
	created := inserted || res.Changed > 0
	if created {
		_ = s.events.Enqueue(room.ID, foundingEvents(room, res.Members, count, counted, f))
	}
	return room, created, nil
}

func (s *Service) insertRoom(ctx context.Context, r domain.Room) (domain.Room, bool, error) {
	err := s.rooms.InsertRoom(ctx, r)
	switch {
	case err == nil:
		r.MemberCount, r.MemberCountVer = 0, 0
		return r, true, nil
	case !errors.Is(err, store.ErrRoomExists):
		return domain.Room{}, false, err
	}
	stored, err := s.rooms.Get(ctx, r.ID)
	if err != nil {
		return domain.Room{}, false, err
	}
	if r.Type != domain.RoomDM || stored.Tenant != r.Tenant || stored.DMKey != r.DMKey {
		return domain.Room{}, false, errRoomIDTaken
	}
	return stored, false, nil
}

func (s *Service) settleCount(ctx context.Context, room uint64, t work.Timer, delta int) (domain.MemberCount, bool) {
	if delta == 0 {
		s.timers.Disarm(ctx, t)
		return domain.MemberCount{}, false
	}
	c, err := s.members.AddMemberCount(ctx, room, delta)
	if err != nil {
		s.log.WarnContext(ctx, "member count of a new room not updated; the check timer will recount", "room", room, "delta", delta, "err", err)
		return domain.MemberCount{}, false
	}
	s.timers.Disarm(ctx, t)
	return c, true
}

func (s *Service) disarm(ctx context.Context, t work.Timer) {
	settle, done := settling(ctx)
	defer done()
	s.timers.Disarm(settle, t)
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
