package member

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

type ClearCmd struct {
	Tenant, User string
	Room         uint64
}

func (s *Service) ClearHistory(ctx context.Context, c ClearCmd) (time.Time, error) {
	req, err := s.d.Access.Authorize(ctx, access.ClearHistory, c.Tenant, c.User, c.Room)
	if err != nil {
		return time.Time{}, err
	}
	now := s.now()
	at, rose, err := s.d.Rooms.ClearHistory(ctx, c.Room, c.User, now)
	if err != nil {
		return time.Time{}, err
	}
	if rose {
		s.tell(c.Room, pbconv.HistoryCleared(req.Room, c.User, at, now))
	}
	return at, nil
}
