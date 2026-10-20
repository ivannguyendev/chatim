package room

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errBadRequestID = fmt.Errorf("%w: request_id", apperr.ErrInvalidArgument)

type CreateRoomCmd struct {
	Tenant, User string
	Type         domain.RoomType
	Name         string
	Members      []string
	RequestID    string
}

func (s *Service) CreateRoom(ctx context.Context, c CreateRoomCmd) (domain.Room, error) {
	if domain.ValidCID(c.RequestID) != nil {
		return domain.Room{}, errBadRequestID
	}
	if len(c.Members) > s.d.Members.MemberBatch() {
		return domain.Room{}, domain.ErrTooManyMembers
	}
	now := s.now()
	room, members, err := domain.NewRoom(c.Tenant, c.User, c.Type, c.Name, c.Members, now, s.d.NewID())
	if err != nil {
		return domain.Room{}, err
	}
	f := founding{room: room, members: members, by: c.User, at: now}
	key := dedupe.CreateKey(c.Tenant, c.User, c.RequestID)
	status, rec, err := s.d.Requests.Begin(ctx, key)
	switch {
	case err != nil:
		return domain.Room{}, err
	case status == dedupe.RequestBusy:
		return domain.Room{}, domain.ErrRetryLater
	case status == dedupe.RequestDone:
		return s.resumeRoom(ctx, f, rec.Seq)
	}
	claimed := false
	f.claimed = func(r domain.Room) {
		claimed = true
		settle, done := member.Settling(ctx)
		defer done()
		s.d.Requests.Finish(settle, key, dedupe.Record{Seq: r.ID, CreatedAt: now})
	}
	got, _, err := s.ensureRoom(ctx, f)
	if err != nil && !claimed {
		settle, done := member.Settling(ctx)
		defer done()
		s.d.Requests.Cancel(settle, key)
	}
	if err != nil {
		return domain.Room{}, err
	}
	return got, nil
}

func (s *Service) resumeRoom(ctx context.Context, f founding, id uint64) (domain.Room, error) {
	stored, err := s.d.Rooms.Get(ctx, id)
	if err != nil {
		return domain.Room{}, err
	}
	if err := domain.CheckTenant(stored, f.room.Tenant); err != nil {
		return domain.Room{}, err
	}
	missing, err := s.unwritten(ctx, id, f.members)
	if err != nil || len(missing) == 0 {
		return stored, err
	}
	f.room.ID, f.members, f.resume = id, missing, true
	got, _, err := s.ensureRoom(ctx, f)
	if err != nil {
		return domain.Room{}, err
	}
	return got, nil
}

func (s *Service) unwritten(ctx context.Context, room uint64, founders []domain.Member) ([]domain.Member, error) {
	users := make([]string, len(founders))
	for i, m := range founders {
		users[i] = m.User
	}
	docs, err := s.d.Members.MembersOf(ctx, room, users)
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
