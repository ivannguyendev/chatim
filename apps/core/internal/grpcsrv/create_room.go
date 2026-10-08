package grpcsrv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const createAttempts = 3

var errRoomIDsTaken = fmt.Errorf("room id taken on all %d attempts: %w", createAttempts, apperr.ErrUnavailable)

func (s *Service) CreateRoom(ctx context.Context, req *chatimv1.CreateRoomRequest) (*chatimv1.CreateRoomResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	typ, err := pbconv.DomainRoomType(req.GetType())
	if err != nil {
		return nil, err
	}
	if len(req.GetMembers()) > s.mutator.MemberBatch() {
		return nil, domain.ErrTooManyMembers
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	for range createAttempts {
		room, members, err := domain.NewRoom(who.tenant, who.user, typ, req.GetName(), req.GetMembers(), now, s.newID())
		if err != nil {
			return nil, err
		}
		err = s.rooms.Create(ctx, room, members)
		switch {
		case err == nil:
			_ = s.events.Enqueue(room.ID, creationEvents(room, members))
			return &chatimv1.CreateRoomResponse{Room: pbconv.Room(room)}, nil
		case errors.Is(err, store.ErrRoomExists):
			s.log.WarnContext(ctx, "room id taken, drawing a new one", "room", room.ID)
		default:
			return nil, err
		}
	}
	return nil, errRoomIDsTaken
}

func creationEvents(room domain.Room, members []domain.Member) []*chatimv1.Event {
	events := make([]*chatimv1.Event, 0, len(members)+1)
	events = append(events, pbconv.RoomCreated(room))
	for _, m := range members {
		if ev := pbconv.MemberEvent(room.Type, m); ev != nil {
			events = append(events, ev)
		}
	}
	return events
}
