package room

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errDirectRoomInfo = fmt.Errorf("direct room info is fixed: %w", apperr.ErrFailedPrecondition)

type UpdateRoomCmd struct {
	Tenant, User string
	Room         uint64
	Update       domain.RoomUpdate
}

type RoomResult struct {
	Room    domain.Room
	Changed bool
}

func (s *Service) UpdateRoom(ctx context.Context, c UpdateRoomCmd) (RoomResult, error) {
	if err := domain.ValidateRoomUpdate(c.Update); err != nil {
		return RoomResult{}, err
	}
	req, err := s.d.Access.Admit(ctx, access.UpdateRoom, c.Tenant, c.User, c.Room)
	if err != nil {
		return RoomResult{}, err
	}
	if req.Room.Type == domain.RoomDM {
		return RoomResult{}, errDirectRoomInfo
	}
	if err := s.d.Access.Allow(ctx, req); err != nil {
		return RoomResult{}, err
	}
	next, changed := c.Update.Apply(req.Room)
	if !changed {
		return RoomResult{Room: req.Room}, nil
	}
	at := s.now()
	ok, err := s.d.Rooms.UpdateRoomInfo(ctx, req.Room.ID, req.Room.InfoVer, store.RoomInfoChange{Update: c.Update, By: c.User, At: at})
	switch {
	case err != nil:
		return RoomResult{}, err
	case !ok:
		return RoomResult{}, domain.ErrRetryLater
	}
	next.UpdatedAt, next.UpdatedBy = at, c.User
	s.tell(next.ID, pbconv.RoomUpdated(next))
	return RoomResult{Room: next, Changed: true}, nil
}
