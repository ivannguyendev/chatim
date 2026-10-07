package memstore

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Hidden = (*Hidden)(nil)

type hiddenKey struct {
	user string
	key  store.MsgKey
}

type Hidden struct {
	mu   sync.RWMutex
	seqs map[hiddenKey]time.Time
	log  *Messages
}

func NewHidden() *Hidden { return &Hidden{seqs: make(map[hiddenKey]time.Time)} }

func (s *Hidden) Hide(ctx context.Context, user string, key store.MsgKey, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	if err := store.ValidateMarkTime(at); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := hiddenKey{user: user, key: key}
	if _, ok := s.seqs[k]; ok {
		return nil
	}
	at = time.UnixMilli(at.UnixMilli()).UTC()
	s.seqs[k] = at
	if s.log != nil {
		s.log.appendFact(logged{kind: store.MessageHidden, hidden: domain.HiddenMessage{User: user, Room: key.Room, Thread: key.Thread, Seq: key.Seq, At: at}})
	}
	return nil
}

func (s *Hidden) HiddenIn(ctx context.Context, user string, room, thread, from, to uint64) ([]uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []uint64
	for k := range s.seqs {
		if k.user == user && k.key.Room == room && k.key.Thread == thread && k.key.Seq >= from && k.key.Seq <= to {
			out = append(out, k.key.Seq)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (s *Hidden) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.HiddenMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxHiddenScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.HiddenMessage{}
	for k, at := range s.seqs {
		if k.key.Room == room && !at.Before(from) && !at.After(to) {
			out = append(out, domain.HiddenMessage{User: k.user, Room: room, Thread: k.key.Thread, Seq: k.key.Seq, At: at})
		}
	}
	slices.SortFunc(out, byHideTimeThenKey)
	return out[:min(len(out), limit)], nil
}

func byHideTimeThenKey(a, b domain.HiddenMessage) int {
	return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.User, b.User), cmp.Compare(a.Thread, b.Thread), cmp.Compare(a.Seq, b.Seq))
}
