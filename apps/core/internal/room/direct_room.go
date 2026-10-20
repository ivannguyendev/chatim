package room

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type OpenDirectCmd struct {
	Tenant, User, Other string
}

func (s *Service) OpenDirectRoom(ctx context.Context, c OpenDirectCmd) (domain.Room, bool, error) {
	now, candidate := s.now(), s.d.NewID()
	room, members, err := domain.NewDirectRoom(c.Tenant, c.User, c.Other, now, candidate)
	if err != nil {
		return domain.Room{}, false, err
	}
	if err := s.d.Access.Allow(ctx, access.Request{Action: access.OpenDirect, User: c.User}); err != nil {
		return domain.Room{}, false, err
	}
	if room.ID, err = s.d.Directs.Claim(ctx, c.Tenant, c.User, c.Other, candidate, now); err != nil {
		return domain.Room{}, false, err
	}
	got, created, err := s.openDirect(ctx, founding{room: room, members: members, by: c.User, at: now})
	if errors.Is(err, errRoomIDTaken) {
		s.repoint(ctx, c.Tenant, c.User, c.Other, room.ID)
	}
	if err != nil {
		return domain.Room{}, false, err
	}
	return got, created, nil
}

func (s *Service) openDirect(ctx context.Context, f founding) (domain.Room, bool, error) {
	stored, err := s.d.Rooms.Get(ctx, f.room.ID)
	switch {
	case errors.Is(err, domain.ErrRoomNotFound):
		return s.ensureRoom(ctx, f)
	case err != nil:
		return domain.Room{}, false, err
	case stored.Tenant != f.room.Tenant || stored.DMKey != f.room.DMKey:
		return domain.Room{}, false, errRoomIDTaken
	}
	_, users := f.join(f.room.ID)
	pair, err := s.d.Members.MembersOf(ctx, f.room.ID, users)
	if err != nil {
		return domain.Room{}, false, err
	}
	if activeMembers(pair) < len(users) {
		return s.ensureRoom(ctx, f)
	}
	return stored, false, nil
}

func activeMembers(docs []domain.Member) int {
	n := 0
	for _, m := range docs {
		if m.Active() {
			n++
		}
	}
	return n
}

func (s *Service) repoint(ctx context.Context, tenant, a, b string, taken uint64) {
	next := s.d.NewID()
	moved, err := s.d.Directs.Repoint(ctx, tenant, a, b, taken, next)
	switch {
	case err != nil:
		s.d.Log.WarnContext(ctx, "direct room claim not moved off a taken room id", "room", taken, "next", next, "err", err)
	case moved:
		s.d.Log.WarnContext(ctx, "direct room claim moved off a taken room id", "room", taken, "next", next)
	default:
		s.d.Log.DebugContext(ctx, "direct room claim already moved by another opener", "room", taken)
	}
}
