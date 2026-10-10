package grpcsrv

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) OpenDirectRoom(ctx context.Context, req *chatimv1.OpenDirectRoomRequest) (*chatimv1.OpenDirectRoomResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	other, now, candidate := req.GetOtherUser(), s.now().UTC().Truncate(time.Millisecond), s.newID()
	room, members, err := domain.NewDirectRoom(who.tenant, who.user, other, now, candidate)
	if err != nil {
		return nil, err
	}
	if err := s.access.Allow(ctx, access.Request{Action: access.OpenDirect, User: who.user}); err != nil {
		return nil, err
	}
	if room.ID, err = s.directs.Claim(ctx, who.tenant, who.user, other, candidate, now); err != nil {
		return nil, err
	}
	got, created, err := s.openDirect(ctx, founding{room: room, members: members, by: who.user, at: now})
	if errors.Is(err, errRoomIDTaken) {
		s.repoint(ctx, who.tenant, who.user, other, room.ID)
	}
	if err != nil {
		return nil, err
	}
	return &chatimv1.OpenDirectRoomResponse{Room: pbconv.Room(got), Created: created}, nil
}

func (s *Service) openDirect(ctx context.Context, f founding) (domain.Room, bool, error) {
	stored, err := s.rooms.Get(ctx, f.room.ID)
	switch {
	case errors.Is(err, domain.ErrRoomNotFound):
		return s.ensureRoom(ctx, f)
	case err != nil:
		return domain.Room{}, false, err
	case stored.Tenant != f.room.Tenant || stored.DMKey != f.room.DMKey:
		return domain.Room{}, false, errRoomIDTaken
	}
	_, users := f.join(f.room.ID)
	pair, err := s.members.MembersOf(ctx, f.room.ID, users)
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
	next := s.newID()
	moved, err := s.directs.Repoint(ctx, tenant, a, b, taken, next)
	switch {
	case err != nil:
		s.log.WarnContext(ctx, "direct room claim not moved off a taken room id", "room", taken, "next", next, "err", err)
	case moved:
		s.log.WarnContext(ctx, "direct room claim moved off a taken room id", "room", taken, "next", next)
	default:
		s.log.DebugContext(ctx, "direct room claim already moved by another opener", "room", taken)
	}
}
