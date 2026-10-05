package memstore

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Rooms) TouchActivity(ctx context.Context, acts []store.Activity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range acts {
		if r, ok := s.rooms[a.Room]; ok {
			s.rooms[a.Room] = withActivity(r, a)
		}
	}
	return nil
}

func (s *Rooms) ActiveRooms(ctx context.Context, q store.ActiveQuery) ([]domain.Room, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Room{}
	for _, r := range s.rooms {
		if r.ID > q.After && (q.Tenant == "" || r.Tenant == q.Tenant) && activeFor(r, q) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b domain.Room) int { return cmp.Compare(a.ID, b.ID) })
	return out[:min(len(out), q.Limit)], nil
}

func withActivity(r domain.Room, a store.Activity) domain.Room {
	at := a.At.UTC()
	r.LastChangeAt = laterOf(r.LastChangeAt, at)
	if a.Thread == 0 {
		r.LastSeq = max(r.LastSeq, a.Seq)
		r.LastMsgAt = laterOf(r.LastMsgAt, at)
	}
	return r
}

func activeFor(r domain.Room, q store.ActiveQuery) bool {
	created := !r.CreatedAt.Before(q.From) && !r.CreatedAt.After(q.To)
	touched := !r.LastChangeAt.IsZero() && store.HourBucket(r.LastChangeAt) >= store.HourBucket(q.From)
	return created || touched
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
