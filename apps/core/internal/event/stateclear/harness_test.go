package stateclear_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/stateclear"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	clearFailedMsg = "pending state clear failed; the sweeper republishes these events"
	queueFullMsg   = "pending state clear queue full; dropping clears"
)

var (
	errStore    = errors.New("store down")
	defaultCfg  = stateclear.Config{}
	fastFlush   = stateclear.Config{Flush: 100 * time.Millisecond}
	smallBuffer = stateclear.Config{Batch: 2, Queue: 2}
)

type fakeStore struct {
	mu    sync.Mutex
	calls [][]store.StateTarget
	fail  error
	block bool
}

func (f *fakeStore) Clear(ctx context.Context, targets []store.StateTarget) error {
	f.mu.Lock()
	f.calls = append(f.calls, slices.Clone(targets))
	fail, block := f.fail, f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return fail
}

func (f *fakeStore) set(fail error, block bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail, f.block = fail, block
}

func (f *fakeStore) sizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.calls))
	for i, c := range f.calls {
		out[i] = len(c)
	}
	return out
}

func (f *fakeStore) seqs(call int) []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint64, len(f.calls[call]))
	for i, t := range f.calls[call] {
		out[i] = t.Seq
	}
	return out
}

type rig struct {
	*stateclear.Queue
	st   *fakeStore
	sink *testlog.Sink
	done chan error
}

func newRig(t *testing.T, cfg stateclear.Config) *rig {
	t.Helper()
	rg := &rig{st: &fakeStore{}, sink: &testlog.Sink{}, done: make(chan error, 1)}
	q, err := stateclear.New(rg.st, cfg, rg.sink.Logger())
	if err != nil {
		t.Fatalf("stateclear.New: %v", err)
	}
	rg.Queue = q
	return rg
}

func started(t *testing.T, cfg stateclear.Config) *rig {
	t.Helper()
	rg := newRig(t, cfg)
	go func() { rg.done <- rg.Run(context.Background()) }()
	synctest.Wait()
	return rg
}

func (rg *rig) add(seqs ...uint64) {
	for _, s := range seqs {
		rg.Add(target(s))
	}
}

func (rg *rig) closeAndWait(t *testing.T) {
	t.Helper()
	if err := rg.Close(context.Background()); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if err := <-rg.done; err != nil {
		t.Fatalf("Run after Close = %v", err)
	}
}

func (rg *rig) expectCounts(t *testing.T, cleared, dropped, failures uint64) {
	t.Helper()
	if c, d, f := rg.Cleared(), rg.Dropped(), rg.Failures(); c != cleared || d != dropped || f != failures {
		t.Fatalf("cleared/dropped/failures = %d/%d/%d, want %d/%d/%d", c, d, f, cleared, dropped, failures)
	}
}

func expectSizes(t *testing.T, st *fakeStore, want ...int) {
	t.Helper()
	if got := st.sizes(); !slices.Equal(got, want) {
		t.Fatalf("Clear batch sizes = %v, want %v", got, want)
	}
}

func target(seq uint64) store.StateTarget {
	return store.StateTarget{
		Collection: store.StateMessages, Bit: store.BitMsgCreated,
		Room: 7, Seq: seq, At: time.UnixMilli(1_700_000_000_000).UTC(),
	}
}

func seqRange(from, to uint64) []uint64 {
	out := make([]uint64, 0, to-from+1)
	for s := from; s <= to; s++ {
		out = append(out, s)
	}
	return out
}
