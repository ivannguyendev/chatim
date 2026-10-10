package memstore

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Interactions) AddReply(ctx context.Context, r domain.Reply) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateReply(r); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := replyKey{parent: store.MsgKey(r.Parent), reply: store.ReplyKeyOf(r)}
	if _, ok := s.replies[k]; ok {
		return false, nil
	}
	r.Live, r.Ver = true, 1
	s.replies[k] = r
	return true, nil
}

func (s *Interactions) RemoveReply(ctx context.Context, r domain.Reply, at time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateReply(r); err != nil {
		return false, err
	}
	if err := store.ValidateMarkTime(at); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := replyKey{parent: store.MsgKey(r.Parent), reply: store.ReplyKeyOf(r)}
	cur, ok := s.replies[k]
	switch {
	case !ok:
		r.Live, r.Ver, r.At = false, 1, at
		s.replies[k] = r
		return false, nil
	case !cur.Live:
		return false, nil
	}
	cur.Live, cur.Ver, cur.At = false, cur.Ver+1, at
	s.replies[k] = cur
	return true, nil
}

func (s *Interactions) Replies(ctx context.Context, parent store.MsgKey, afterSeq uint64, limit int) ([]domain.Reply, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := parent.Validate(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxPageLimit); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Reply{}
	for k, r := range s.replies {
		if k.parent == parent && r.Live && k.reply.Thread == 0 && k.reply.Seq > afterSeq {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b domain.Reply) int { return cmp.Compare(a.Seq, b.Seq) })
	return out[:min(len(out), limit)], nil
}

func (s *Interactions) CountLiveReplies(ctx context.Context, parent store.MsgKey) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := parent.Validate(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n uint32
	for k, r := range s.replies {
		if k.parent == parent && r.Live {
			n++
		}
	}
	return n, nil
}
