package roomcache

import (
	"context"
	"strconv"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type TailFetch func(ctx context.Context, room, afterSeq uint64) (Tail, error)

func (c *TailCache) Load(ctx context.Context, a store.RoomActivity, fetch TailFetch) (Tail, error) {
	if t, state := c.Lookup(a); state == TailFresh {
		return t, nil
	}
	v, err, _ := c.flight.Do(flightKey(a), func() (any, error) { return c.fill(ctx, a, fetch) })
	if err != nil {
		return Tail{}, err
	}
	return v.(Tail), nil
}

func (c *TailCache) fill(ctx context.Context, a store.RoomActivity, fetch TailFetch) (Tail, error) {
	base, state := c.check(a)
	if state == TailFresh {
		return base, nil
	}
	newer, err := fetch(ctx, a.Room, base.LastSeq)
	if err != nil {
		return Tail{}, err
	}
	newer.LastSeq, newer.LastEditAt = max(newer.LastSeq, a.LastSeq), a.LastEditAt
	if state == TailReload {
		return c.Replace(a.Room, newer), nil
	}
	if merged, ok := c.Merge(a.Room, newer); ok {
		return merged, nil
	}
	return mergeTails(base, newer), nil
}

func flightKey(a store.RoomActivity) string {
	b := strconv.AppendUint(nil, a.Room, 10)
	b = append(b, '-')
	b = strconv.AppendUint(b, a.LastSeq, 10)
	b = append(b, '-')
	b = strconv.AppendInt(b, a.LastEditAt.Unix(), 10)
	b = append(b, '.')
	b = strconv.AppendInt(b, int64(a.LastEditAt.Nanosecond()), 10)
	return string(b)
}
