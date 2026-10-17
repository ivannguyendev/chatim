package roomcache

import (
	"context"
	"strconv"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type FetchNewestTailAfterSeq func(ctx context.Context, room, afterSeq uint64) (Tail, error)

func (c *TailCache) Load(ctx context.Context, a store.RoomActivity, fetchNewestAfter FetchNewestTailAfterSeq) (Tail, error) {
	if t, state := c.Lookup(a); state == TailFresh {
		return t, nil
	}
	v, err, _ := c.flight.Do(flightKey(a), func() (any, error) { return c.fill(ctx, a, fetchNewestAfter) })
	if err != nil {
		return Tail{}, err
	}
	return cloneTail(v.(Tail)), nil
}

func (c *TailCache) fill(ctx context.Context, a store.RoomActivity, fetchNewestAfter FetchNewestTailAfterSeq) (Tail, error) {
	base, state := c.check(a)
	if state == TailFresh {
		return base, nil
	}
	newer, err := fetchNewestAfter(ctx, a.Room, base.LastSeq)
	if err != nil {
		return Tail{}, err
	}
	newer.LastSeq, newer.LastEditAt = max(newer.LastSeq, a.LastSeq), a.LastEditAt
	if state == TailReload || len(newer.Window) >= TailWindow {
		return c.replace(a.Room, newer), nil
	}
	if merged, ok := c.merge(a.Room, newer); ok {
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
