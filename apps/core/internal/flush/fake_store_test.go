package flush_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type batch struct {
	msgs []domain.Message
	at   time.Time
}

type fakeStore struct {
	store.Messages
	shards  int
	gate    chan struct{}
	respond func([]domain.Message) []store.Result

	mu       sync.Mutex
	batches  []batch
	inflight map[int]int
	peak     map[int]int
}

func (s *fakeStore) Insert(ctx context.Context, msgs []domain.Message) []store.Result {
	shard := s.enter(msgs)
	defer s.leave(shard)
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return uniform(len(msgs), store.Unknown, ctx.Err())
		}
	}
	if s.respond != nil {
		return s.respond(msgs)
	}
	return uniform(len(msgs), store.Inserted, nil)
}

func (s *fakeStore) enter(msgs []domain.Message) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight == nil {
		s.inflight, s.peak = map[int]int{}, map[int]int{}
	}
	shard := shardOf(msgs[0].Room, max(s.shards, 1))
	s.inflight[shard]++
	s.peak[shard] = max(s.peak[shard], s.inflight[shard])
	s.batches = append(s.batches, batch{msgs: slices.Clone(msgs), at: time.Now()})
	return shard
}

func (s *fakeStore) leave(shard int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight[shard]--
}

func (s *fakeStore) all() []batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.batches)
}

func (s *fakeStore) inFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.inflight {
		n += v
	}
	return n
}

func (s *fakeStore) peaks() map[int]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.peak)
}

type recorder struct {
	mu    sync.Mutex
	calls map[string]int
	got   map[string][]store.Result
}

func newRecorder() *recorder {
	return &recorder{calls: map[string]int{}, got: map[string][]store.Result{}}
}

func (r *recorder) group(id string, room uint64, seqs ...uint64) flush.Group {
	msgs := make([]domain.Message, len(seqs))
	for i, seq := range seqs {
		msgs[i] = domain.Message{Room: room, Seq: seq, CID: id}
	}
	return flush.Group{Room: room, Msgs: msgs, Done: func(res []store.Result) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls[id]++
		r.got[id] = res
	}}
}

func (r *recorder) results(t *testing.T, id string) []store.Result {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := r.calls[id]; n != 1 {
		t.Fatalf("group %s: Done called %d times, want 1", id, n)
	}
	return r.got[id]
}

func (r *recorder) assertCalledOnce(t *testing.T, ids ...string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) != len(ids) {
		t.Errorf("Done called for %d groups, want %d", len(r.calls), len(ids))
	}
	for _, id := range ids {
		if n := r.calls[id]; n != 1 {
			t.Errorf("group %s: Done called %d times, want 1", id, n)
		}
	}
}

func uniform(n int, o store.Outcome, err error) []store.Result {
	out := make([]store.Result, max(n, 0))
	for i := range out {
		out[i] = store.Result{Outcome: o, Err: err}
	}
	return out
}

func bySeq(msgs []domain.Message) []store.Result {
	out := make([]store.Result, len(msgs))
	for i, m := range msgs {
		out[i] = expected(m.Room, m.Seq)
	}
	return out
}

func expected(room, seq uint64) store.Result {
	o := store.Outcome(seq%4 + 1)
	if o == store.Inserted || o == store.Duplicate {
		return store.Result{Outcome: o}
	}
	return store.Result{Outcome: o, Err: fmt.Errorf("room %d seq %d", room, seq)}
}

func sameResult(a, b store.Result) bool {
	if a.Outcome != b.Outcome || (a.Err == nil) != (b.Err == nil) {
		return false
	}
	return a.Err == nil || a.Err.Error() == b.Err.Error()
}

func shardOf(room uint64, shards int) int { return int(slotmap.Of(room)) % shards }

func roomsIn(shard, shards, n int) []uint64 {
	var out []uint64
	for room := uint64(1); len(out) < n; room++ {
		if shardOf(room, shards) == shard {
			out = append(out, room)
		}
	}
	return out
}
