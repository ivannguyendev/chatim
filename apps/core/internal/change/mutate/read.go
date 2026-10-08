package mutate

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errUnreadFromZero = fmt.Errorf("%w: unread from seq 0", apperr.ErrInvalidArgument)

type ReadCmd struct {
	Tenant, User string
	Room, Seq    uint64
}

func (m *Mutator) MarkRead(ctx context.Context, c ReadCmd) (domain.ReadPosition, error) {
	return m.moveRead(ctx, c, false)
}

func (m *Mutator) MarkUnread(ctx context.Context, c ReadCmd) (domain.ReadPosition, error) {
	if c.Seq == 0 {
		return domain.ReadPosition{}, errUnreadFromZero
	}
	return m.moveRead(ctx, c, true)
}

func (m *Mutator) moveRead(ctx context.Context, c ReadCmd, back bool) (domain.ReadPosition, error) {
	req, err := m.d.Access.Authorize(ctx, access.MarkRead, c.Tenant, c.User, c.Room)
	if err != nil {
		return domain.ReadPosition{}, err
	}
	seq, err := m.clampRead(ctx, req.Room, c.Seq)
	if err != nil {
		return domain.ReadPosition{}, err
	}
	if seq == 0 {
		return domain.ReadPosition{Seq: req.Member.ReadSeq, Ver: req.Member.ReadVer}, nil
	}
	var pos domain.ReadPosition
	var moved bool
	if back {
		pos, moved, err = m.d.Reads.MarkUnread(ctx, c.Room, c.User, seq-1)
	} else {
		pos, moved, err = m.d.Reads.MarkRead(ctx, c.Room, c.User, seq)
	}
	if err != nil {
		return domain.ReadPosition{}, err
	}
	if moved {
		m.tell(req.Room.ID, pbconv.ReadUpdated(req.Room, c.User, pos, m.now()))
	}
	return pos, nil
}

func (m *Mutator) clampRead(ctx context.Context, r domain.Room, seq uint64) (uint64, error) {
	if seq > 0 && seq <= r.LastSeq {
		return seq, nil
	}
	last, err := m.d.Messages.Last(ctx, r.ID, 0)
	if err != nil {
		return 0, err
	}
	if seq == 0 || seq > last {
		return last, nil
	}
	return seq, nil
}

func (m *Mutator) tell(room uint64, ev *chatimv1.Event) {
	_ = m.d.Events.Enqueue(room, []*chatimv1.Event{ev})
}
