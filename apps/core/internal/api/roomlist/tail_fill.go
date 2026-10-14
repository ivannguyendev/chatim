package roomlist

import (
	"context"
	"math"

	"golang.org/x/sync/errgroup"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (l *Lister) loadTails(ctx, stop context.Context, rooms []resolved) ([]roomcache.Tail, error) {
	out := make([]roomcache.Tail, len(rooms))
	g, loading := errgroup.WithContext(stop)
	g.SetLimit(TailFills)
	for i, r := range rooms {
		g.Go(func() error {
			if err := loading.Err(); err != nil {
				return err
			}
			t, err := l.tail.Load(ctx, r.activity, l.fetchNewestWindowAfterSeq)
			if err != nil {
				return storeErr("load room tail", err)
			}
			out[i] = t
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return l.attachPreviews(stop, rooms, out)
}

func (l *Lister) fetchNewestWindowAfterSeq(ctx context.Context, room, afterSeq uint64) (roomcache.Tail, error) {
	window, err := l.tails.TailWindow(ctx, room, afterSeq, roomcache.TailWindow)
	return roomcache.Tail{Window: window}, err
}

func (l *Lister) attachPreviews(ctx context.Context, rooms []resolved, tails []roomcache.Tail) ([]roomcache.Tail, error) {
	var keys []store.MsgKey
	for i, t := range tails {
		for _, seq := range t.MissingPreviews() {
			keys = append(keys, store.MsgKey{Room: rooms[i].activity.Room, Seq: seq})
		}
	}
	if len(keys) == 0 {
		return tails, nil
	}
	previews, err := l.tails.TailPreviews(ctx, keys)
	if err != nil {
		return nil, storeErr("read room previews", err)
	}
	byRoom := make(map[uint64][]domain.Message, len(rooms))
	for _, m := range previews {
		byRoom[m.Room] = append(byRoom[m.Room], m)
	}
	for i, t := range tails {
		if found := byRoom[rooms[i].activity.Room]; len(found) > 0 {
			tails[i], _ = l.tail.AttachPreviews(rooms[i].activity.Room, t, found)
		}
	}
	return tails, nil
}

func hiddenRanges(rooms []resolved) []store.HiddenRange {
	out := make([]store.HiddenRange, len(rooms))
	for i, r := range rooms {
		from := uint64(1)
		if last := r.activity.LastSeq; last > roomcache.TailWindow {
			from = last - roomcache.TailWindow + 1
		}
		out[i] = store.HiddenRange{Room: r.room.ID, From: from, To: math.MaxInt64}
	}
	return out
}

func hiddenSet(seqs []uint64) map[uint64]struct{} {
	if len(seqs) == 0 {
		return nil
	}
	out := make(map[uint64]struct{}, len(seqs))
	for _, s := range seqs {
		out[s] = struct{}{}
	}
	return out
}

func viewerOf(w who, me domain.Member, hidden []uint64) domain.RoomViewer {
	return domain.RoomViewer{User: w.user, ReadSeq: me.ReadSeq, ClearedAt: me.ClearedAt, Hidden: hiddenSet(hidden)}
}
