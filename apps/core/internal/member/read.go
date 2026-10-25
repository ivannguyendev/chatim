package member

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errUnreadFromZero = fmt.Errorf("%w: unread from seq 0", apperr.ErrInvalidArgument)

type ReadCmd struct {
	Tenant, User string
	Room, Seq    uint64
}

func (s *Service) MarkRead(ctx context.Context, c ReadCmd) (domain.ReadPosition, error) {
	return s.moveRead(ctx, c, false)
}

func (s *Service) MarkUnread(ctx context.Context, c ReadCmd) (domain.ReadPosition, error) {
	if c.Seq == 0 {
		return domain.ReadPosition{}, errUnreadFromZero
	}
	return s.moveRead(ctx, c, true)
}

func (s *Service) moveRead(ctx context.Context, c ReadCmd, back bool) (domain.ReadPosition, error) {
	req, err := s.d.Access.Authorize(ctx, access.MarkRead, c.Tenant, c.User, c.Room)
	if err != nil {
		return domain.ReadPosition{}, err
	}
	seq, err := s.clampRead(ctx, req.Room, c.Seq)
	if err != nil {
		return domain.ReadPosition{}, err
	}
	if seq == 0 {
		return domain.ReadPosition{Seq: req.Member.ReadSeq, Ver: req.Member.ReadVer}, nil
	}
	var pos domain.ReadPosition
	var moved bool
	if back {
		pos, moved, err = s.d.Reads.MarkUnread(ctx, c.Room, c.User, seq-1)
	} else {
		pos, moved, err = s.d.Reads.MarkRead(ctx, c.Room, c.User, seq)
	}
	if err != nil {
		return domain.ReadPosition{}, err
	}
	if moved {
		s.d.ReadSink.Add(req.Room, c.User, pos)
	}
	return pos, nil
}

func (s *Service) clampRead(ctx context.Context, r domain.Room, seq uint64) (uint64, error) {
	if seq > 0 && seq <= r.LastSeq {
		return seq, nil
	}
	last, err := s.d.Messages.Last(ctx, r.ID, 0)
	if err != nil {
		return 0, err
	}
	if seq == 0 || seq > last {
		return last, nil
	}
	return seq, nil
}
