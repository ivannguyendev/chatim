package sweep

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type roomRebuilder struct {
	rooms eventkit.RoomReader
	now   func() time.Time
}

func NewRoomRebuilder(rooms eventkit.RoomReader, now func() time.Time) Rebuilder {
	if now == nil {
		now = time.Now
	}
	return &roomRebuilder{rooms: rooms, now: now}
}

func (r *roomRebuilder) Rebuild(ctx context.Context, doc store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
	room, err := r.rooms.Get(ctx, doc.Room)
	if err != nil {
		return nil, err
	}
	target := store.StateTarget{Collection: store.StateRooms, Bit: bit, Room: doc.Room, Ver: roomStateVersion(room, bit)}
	switch bit {
	case store.BitRoomCreated:
		return one(pbconv.RoomCreated(room)), nil
	case store.BitMemberCount:
		if room.MemberCountVer == 0 {
			return nil, dropWith(target, fmt.Errorf("%w: room %d member count has no version", apperr.ErrInvalidArgument, doc.Room))
		}
		count := domain.MemberCount{Count: room.MemberCount, Ver: room.MemberCountVer}
		return one(pbconv.MemberCountChanged(room, count, "", r.now().UTC().Truncate(time.Millisecond))), nil
	case store.BitRoomInfo:
		if room.InfoVer == 0 {
			return nil, dropWith(target, fmt.Errorf("%w: room %d info has no version", apperr.ErrInvalidArgument, doc.Room))
		}
		return one(pbconv.RoomUpdated(room)), nil
	default:
		return nil, fmt.Errorf("%w: rooms state bit %d", apperr.ErrInvalidArgument, bit)
	}
}

func roomStateVersion(room domain.Room, bit store.StateBit) uint64 {
	switch bit {
	case store.BitMemberCount:
		return room.MemberCountVer
	case store.BitRoomInfo:
		return room.InfoVer
	default:
		return 0
	}
}
