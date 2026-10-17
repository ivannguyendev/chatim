package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

var _ store.Rooms = (*Rooms)(nil)

type roomDoc struct {
	tenant string
	last   time.Time
}

type Rooms struct {
	mu   sync.Mutex
	docs map[uint64]roomDoc
}

func NewRooms() *Rooms {
	return &Rooms{docs: make(map[uint64]roomDoc)}
}

func (s *Rooms) Raise(ctx context.Context, tenant string, room uint64, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateRaise(tenant, room, at); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.docs[room]
	switch {
	case !ok:
		s.docs[room] = roomDoc{tenant: tenant, last: at.UTC()}
	case cur.tenant == tenant && at.After(cur.last):
		s.docs[room] = roomDoc{tenant: tenant, last: at.UTC()}
	}
	return nil
}

func (s *Rooms) Times(ctx context.Context, tenant string, rooms []uint64) (map[uint64]time.Time, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateRooms(tenant, rooms); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[uint64]time.Time)
	for _, id := range rooms {
		if d, ok := s.docs[id]; ok && d.tenant == tenant {
			out[id] = d.last
		}
	}
	return out, nil
}
