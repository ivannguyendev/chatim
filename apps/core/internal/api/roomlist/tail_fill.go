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
	g, stop := errgroup.WithContext(stop)
	g.SetLimit(TailFills)
	for i, r := range rooms {
		g.Go(func() error {
			if err := stop.Err(); err != nil {
				return err
			}
			t, err := l.tail.Load(ctx, r.activity, l.fetchNewestTailAfterSeq)
			if err != nil {
				return storeErr("load room tail", err)
			}
			out[i] = t
			return nil
		})
	}
	return out, g.Wait()
}

func (l *Lister) fetchNewestTailAfterSeq(ctx context.Context, room, afterSeq uint64) (roomcache.Tail, error) {
	window, err := l.tails.TailWindow(ctx, room, afterSeq, roomcache.TailWindow)
	if err != nil || len(window) == 0 {
		return roomcache.Tail{Window: window}, err
	}
	keys := make([]store.MsgKey, 0, roomcache.TailPreviews)
	for _, m := range window[:min(len(window), roomcache.TailPreviews)] {
		keys = append(keys, store.MsgKey{Room: room, Seq: m.Seq})
	}
	previews, err := l.tails.TailPreviews(ctx, keys)
	if err != nil {
		return roomcache.Tail{}, err
	}
	return roomcache.Tail{Window: window, Previews: previews}, nil
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
