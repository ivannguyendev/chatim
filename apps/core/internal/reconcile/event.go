package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var errUndeliverable = errors.New("change cannot become an event")

func (r *Reconciler) message(ctx context.Context, c store.Change) (*nats.Msg, error) {
	typ, err := r.types.get(ctx, c.Msg.Room)
	switch {
	case errors.Is(err, domain.ErrRoomNotFound):
		return nil, fmt.Errorf("%w: room %d: %w", errUndeliverable, c.Msg.Room, err)
	case err != nil:
		return nil, err
	}
	msg, err := publish.Message(r.cfg.SubjectRoot, c.Msg.Room, pbconv.MessageCreated(typ, c.Msg))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUndeliverable, err)
	}
	return msg, nil
}

func (r *Reconciler) acked(ctx context.Context, batch []store.Change) []bool {
	keys := make([]store.MsgKey, len(batch))
	for i, c := range batch {
		keys[i] = store.KeyOf(c.Msg)
	}
	got, err := r.deps.Marks.Acked(ctx, keys)
	if err != nil || len(got) != len(batch) {
		return make([]bool, len(batch))
	}
	return got
}

type roomTypes struct {
	rooms RoomReader
	limit int
	cache map[uint64]domain.RoomType
}

func newRoomTypes(rooms RoomReader, limit int) *roomTypes {
	return &roomTypes{rooms: rooms, limit: limit, cache: make(map[uint64]domain.RoomType)}
}

func (c *roomTypes) get(ctx context.Context, id uint64) (domain.RoomType, error) {
	if t, ok := c.cache[id]; ok {
		return t, nil
	}
	room, err := c.rooms.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if len(c.cache) >= c.limit {
		clear(c.cache)
	}
	c.cache[id] = room.Type
	return room.Type, nil
}
