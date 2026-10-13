package memstore

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.ReplyThreads = (*ReplyThreads)(nil)

type ReplyThreads struct {
	mu      sync.RWMutex
	threads map[store.MsgKey]store.ReplyThread
}

func NewReplyThreads() *ReplyThreads {
	return &ReplyThreads{threads: make(map[store.MsgKey]store.ReplyThread)}
}

func (s *ReplyThreads) Touch(ctx context.Context, parent store.MsgKey, tenant string, replySeq uint64, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateReplyThreadTouch(parent, tenant, replySeq, at); err != nil {
		return err
	}
	at = toMillis(at)
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.threads[parent]
	newer := !ok || at.After(cur.LastReplyAt) || (at.Equal(cur.LastReplyAt) && replySeq > cur.LastReplySeq)
	if !newer {
		return nil
	}
	s.threads[parent] = store.ReplyThread{Parent: parent, Tenant: tenant, LastReplyAt: at, LastReplySeq: replySeq, UpdatedAt: toMillis(time.Now())}
	return nil
}

func (s *ReplyThreads) List(ctx context.Context, room uint64, before store.ReplyThreadCursor, limit int) ([]store.ReplyThread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateReplyThreadList(room, before, limit); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []store.ReplyThread{}
	for k, t := range s.threads {
		if k.Room == room && t.Before(before) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b store.ReplyThread) int {
		return cmp.Or(b.LastReplyAt.Compare(a.LastReplyAt), cmp.Compare(b.Parent.Seq, a.Parent.Seq))
	})
	return out[:min(len(out), limit)], nil
}
