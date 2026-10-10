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
	key := dedupe.CreateKey(who.tenant, who.user, req.GetRequestId())
	status, rec, err := s.requests.Begin(ctx, key)
	switch {
	case err != nil:
		return nil, err
	case status == dedupe.RequestBusy:
		return nil, domain.ErrRetryLater
	case status == dedupe.RequestDone:
		return s.createdRoom(ctx, who, rec.Seq)
	}
	got, _, err := s.ensureRoom(ctx, founding{room: room, members: members, by: who.user, at: now})
	settle, done := settling(ctx)
	defer done()
	if err != nil {
		s.requests.Cancel(settle, key)
		return nil, err
	}
	s.requests.Finish(settle, key, dedupe.Record{Seq: got.ID, CreatedAt: now})
	return &chatimv1.CreateRoomResponse{Room: pbconv.Room(got)}, nil
}

func (s *Service) createdRoom(ctx context.Context, who caller, id uint64) (*chatimv1.CreateRoomResponse, error) {
	room, err := s.rooms.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := domain.CheckTenant(room, who.tenant); err != nil {
		return nil, err
	}
	return &chatimv1.CreateRoomResponse{Room: pbconv.Room(room)}, nil
}
