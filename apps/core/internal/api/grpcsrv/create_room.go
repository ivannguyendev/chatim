package grpcsrv

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errBadRequestID = fmt.Errorf("%w: request_id", apperr.ErrInvalidArgument)

func (s *Service) CreateRoom(ctx context.Context, req *chatimv1.CreateRoomRequest) (*chatimv1.CreateRoomResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	typ, err := pbconv.DomainRoomType(req.GetType())
	if err != nil {
		return nil, err
	}
	if domain.ValidCID(req.GetRequestId()) != nil {
		return nil, errBadRequestID
	}
	if len(req.GetMembers()) > s.mutator.MemberBatch() {
		return nil, domain.ErrTooManyMembers
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	room, members, err := domain.NewRoom(who.tenant, who.user, typ, req.GetName(), req.GetMembers(), now, s.newID())
	if err != nil {
		return nil, err
	}
	f := founding{room: room, members: members, by: who.user, at: now}
	key := dedupe.CreateKey(who.tenant, who.user, req.GetRequestId())
	status, rec, err := s.requests.Begin(ctx, key)
	switch {
	case err != nil:
		return nil, err
	case status == dedupe.RequestBusy:
		return nil, domain.ErrRetryLater
	case status == dedupe.RequestDone:
		return s.resumeRoom(ctx, f, rec.Seq)
	}
	claimed := false
	f.claimed = func(r domain.Room) {
		claimed = true
		settle, done := settling(ctx)
		defer done()
		s.requests.Finish(settle, key, dedupe.Record{Seq: r.ID, CreatedAt: now})
	}
	got, _, err := s.ensureRoom(ctx, f)
	if err != nil && !claimed {
		settle, done := settling(ctx)
		defer done()
		s.requests.Cancel(settle, key)
	}
	if err != nil {
		return nil, err
	}
	return &chatimv1.CreateRoomResponse{Room: pbconv.Room(got)}, nil
}

func (s *Service) resumeRoom(ctx context.Context, f founding, id uint64) (*chatimv1.CreateRoomResponse, error) {
	stored, err := s.rooms.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := domain.CheckTenant(stored, f.room.Tenant); err != nil {
		return nil, err
	}
	missing, err := s.unwritten(ctx, id, f.members)
	if err != nil || len(missing) == 0 {
		return &chatimv1.CreateRoomResponse{Room: pbconv.Room(stored)}, err
	}
	f.room.ID, f.members, f.resume = id, missing, true
	got, _, err := s.ensureRoom(ctx, f)
	if err != nil {
		return nil, err
	}
	return &chatimv1.CreateRoomResponse{Room: pbconv.Room(got)}, nil
}

func (s *Service) unwritten(ctx context.Context, room uint64, founders []domain.Member) ([]domain.Member, error) {
	users := make([]string, len(founders))
	for i, m := range founders {
		users[i] = m.User
	}
	docs, err := s.members.MembersOf(ctx, room, users)
	if err != nil {
		return nil, err
	}
	written := make(map[string]bool, len(docs))
	for _, d := range docs {
		written[d.User] = true
	}
	var missing []domain.Member
	for _, m := range founders {
		if !written[m.User] {
			missing = append(missing, m)
		}
	}
	return missing, nil
}
