package memstore

import (
	"context"
	"slices"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Hidden = (*Hidden)(nil)

type hiddenKey struct {
	user string
	key  store.MsgKey
}

type Hidden struct {
	mu   sync.RWMutex
	seqs map[hiddenKey]struct{}
}

func NewHidden() *Hidden { return &Hidden{seqs: make(map[hiddenKey]struct{})} }

func (s *Hidden) Hide(ctx context.Context, user string, key store.MsgKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seqs[hiddenKey{user: user, key: key}] = struct{}{}
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
