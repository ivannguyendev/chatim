package readbatch

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (q *Queue) Run(ctx context.Context) error {
	if !q.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(q.done)
	ticker := time.NewTicker(q.cfg.Window)
	defer ticker.Stop()
	for {
		select {
		case <-q.stop:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			q.flush(q.swap())
		}
	}
}

func (q *Queue) flush(rooms map[uint64]*pending) {
	if len(rooms) == 0 {
		return
	}
	at := time.Now()
	for id, p := range rooms {
		evs := pages(p, at)
		if err := q.enqueue(id, evs); err != nil {
			q.failures.Add(uint64(len(evs)))
			q.fails.Warn(context.Background(), "read positions enqueue failed; the sweeper republishes them", "room", id, "events", len(evs), "err", err)
			continue
		}
		q.events.Add(uint64(len(evs)))
	}
}

func pages(p *pending, at time.Time) []*chatimv1.Event {
	users := slices.Sorted(maps.Keys(p.users))
	evs := make([]*chatimv1.Event, 0, (len(users)+PageSize-1)/PageSize)
	for chunk := range slices.Chunk(users, PageSize) {
		entries := make([]*chatimv1.ReadEntry, len(chunk))
		for i, u := range chunk {
			pos := p.users[u]
			entries[i] = &chatimv1.ReadEntry{User: u, ReadSeq: pos.Seq, ReadVer: pos.Ver}
		}
		evs = append(evs, pbconv.ReadPositions(p.room, entries, at))
	}
	return evs
}
