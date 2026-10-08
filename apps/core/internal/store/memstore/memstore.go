package memstore

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Messages = (*Messages)(nil)

type timeline struct{ room, thread uint64 }

type Messages struct {
	mu    sync.RWMutex
	lines map[timeline][]domain.Message
	log   []logged
	grew  chan struct{}
}

func NewMessages() *Messages {
	return &Messages{lines: make(map[timeline][]domain.Message), grew: make(chan struct{})}
}

func (s *Messages) Insert(ctx context.Context, msgs []domain.Message) []store.Result {
	out := make([]store.Result, len(msgs))
	if err := ctx.Err(); err != nil {
		for i := range out {
			out[i] = store.Result{Outcome: store.Rejected, Err: err}
		}
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range msgs {
		out[i] = s.insertLocked(m)
	}
	return out
}

func (s *Messages) insertLocked(m domain.Message) store.Result {
	if err := store.KeyOf(m).Validate(); err != nil {
		return store.Result{Outcome: store.Rejected, Err: err}
	}
	tl := timeline{m.Room, m.Thread}
	line := s.lines[tl]
	i, found := slices.BinarySearchFunc(line, m.Seq, bySeq)
	if found {
		return store.Result{Outcome: store.Duplicate}
	}
	m.Hidden, m.Reactions = false, domain.ReactionSummary{}
	s.lines[tl] = slices.Insert(line, i, m)
	s.appendLog(logged{kind: store.MessageInserted, msg: m})
	return store.Result{Outcome: store.Inserted}
}

func (s *Messages) Last(ctx context.Context, room, thread uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.lines[timeline{room, thread}]
	if len(line) == 0 {
		return 0, nil
	}
	return line[len(line)-1].Seq, nil
}

func (s *Messages) Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.lines[timeline{q.Room, q.Thread}]
	lo, hi := window(line, q)
	return slices.Clone(line[lo:hi]), nil
}

func window(line []domain.Message, q store.PageQuery) (lo, hi int) {
	n := len(line)
	switch q.Anchor {
	case store.Latest:
		return max(0, n-q.Limit), n
	case store.Oldest:
		return 0, min(n, q.Limit)
	case store.Before:
		end, _ := slices.BinarySearchFunc(line, q.Seq, bySeq)
		return max(0, end-q.Limit), end
	case store.After:
		start, found := slices.BinarySearchFunc(line, q.Seq, bySeq)
		if found {
			start++
		}
		return start, min(n, start+q.Limit)
	default:
		return 0, 0
	}
}

func (s *Messages) Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateKeys(room, keys); err != nil {
		return nil, err
	}
	wanted := slices.SortedFunc(slices.Values(keys), byThreadSeq)
	wanted = slices.Compact(wanted)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Message, 0, len(wanted))
	for _, k := range wanted {
		line := s.lines[timeline{k.Room, k.Thread}]
		if i, ok := slices.BinarySearchFunc(line, k.Seq, bySeq); ok {
			out = append(out, line[i])
		}
	}
	return out, nil
}

func bySeq(m domain.Message, seq uint64) int { return cmp.Compare(m.Seq, seq) }

func byThreadSeq(a, b store.MsgKey) int {
	return cmp.Or(cmp.Compare(a.Thread, b.Thread), cmp.Compare(a.Seq, b.Seq))
}
