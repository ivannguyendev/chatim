package dedupe

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const settleFullMsg = "cid settle queue full; dropping commits"

var testBatch = BatchConfig{Shards: 2, MaxKeys: 256, Queue: 64}

type fakeRegistry struct {
	mu       sync.Mutex
	gate     chan struct{}
	opened   sync.Once
	err      error
	short    bool
	reserves [][]Key
	commits  [][]Entry
	aborts   [][]Key
}

func (f *fakeRegistry) open() { f.opened.Do(func() { close(f.gate) }) }

func (f *fakeRegistry) fail(err error, short bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err, f.short = err, short
}

func (f *fakeRegistry) Reserve(_ context.Context, keys []Key) ([]Verdict, error) {
	f.mu.Lock()
	f.reserves = append(f.reserves, slices.Clone(keys))
	f.mu.Unlock()
	<-f.gate
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]Verdict, len(keys))
	for i, k := range keys {
		out[i] = fakeVerdict(k)
	}
	if f.short {
		out = out[:len(out)-1]
	}
	return out, nil
}

func (f *fakeRegistry) Commit(_ context.Context, entries []Entry) error {
	f.mu.Lock()
	f.commits = append(f.commits, slices.Clone(entries))
	f.mu.Unlock()
	<-f.gate
	return nil
}

func (f *fakeRegistry) Abort(_ context.Context, keys []Key) error {
	f.mu.Lock()
	f.aborts = append(f.aborts, slices.Clone(keys))
	f.mu.Unlock()
	<-f.gate
	return nil
}

func (f *fakeRegistry) calls() (reserves [][]Key, commits [][]Entry, aborts [][]Key) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reserves), slices.Clone(f.commits), slices.Clone(f.aborts)
}

func startBatcher(t *testing.T, reg Registry, cfg BatchConfig) (*Batcher, *testlog.Sink) {
	t.Helper()
	sink := &testlog.Sink{}
	b, err := NewBatcher(reg, cfg, sink.Logger())
	if err != nil {
		t.Fatalf("NewBatcher: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background()) }()
	t.Cleanup(func() {
		if err := b.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	return b, sink
}

func startFake(t *testing.T, cfg BatchConfig, blocked bool) (*Batcher, *fakeRegistry, *testlog.Sink) {
	t.Helper()
	reg := &fakeRegistry{gate: make(chan struct{})}
	if !blocked {
		reg.open()
	}
	b, sink := startBatcher(t, reg, cfg)
	t.Cleanup(reg.open)
	return b, reg, sink
}

func fakeVerdict(k Key) Verdict {
	n, _ := strconv.ParseUint(k.CID, 10, 64)
	return Verdict{Status: Committed, Record: Record{Seq: k.Room*1000 + n}}
}

func roomsOnShard(shards, shard, n int) []uint64 {
	var out []uint64
	for r := uint64(1); len(out) < n; r++ {
		if int(slotmap.Of(r))%shards == shard {
			out = append(out, r)
		}
	}
	return out
}

func roomKeys(room uint64, n int) []Key {
	out := make([]Key, n)
	for i := range out {
		out[i] = Key{Room: room, User: "alice", CID: strconv.Itoa(i + 1)}
	}
	return out
}

func roomEntries(room uint64, n int) []Entry {
	out := make([]Entry, n)
	for i, k := range roomKeys(room, n) {
		out[i] = Entry{Key: k, Record: sampleRecord}
	}
	return out
}

func reserveAsync(ctx context.Context, b *Batcher, keys []Key) <-chan reserveResult {
	out := make(chan reserveResult, 1)
	go func() {
		v, err := b.Reserve(ctx, keys)
		out <- reserveResult{verdicts: v, err: err}
	}()
	return out
}

func closeAsync(ctx context.Context, b *Batcher) <-chan error {
	out := make(chan error, 1)
	go func() { out <- b.Close(ctx) }()
	return out
}

func expectVerdicts(t *testing.T, got reserveResult, keys []Key) {
	t.Helper()
	if got.err != nil || len(got.verdicts) != len(keys) {
		t.Fatalf("Reserve(%v) = %v, %v", keys, got.verdicts, got.err)
	}
	for i, k := range keys {
		if got.verdicts[i] != fakeVerdict(k) {
			t.Fatalf("verdict %d of %v = %+v, want %+v", i, keys, got.verdicts[i], fakeVerdict(k))
		}
	}
}

func roundSizes[T any](rounds [][]T) []int {
	out := make([]int, len(rounds))
	for i, r := range rounds {
		out[i] = len(r)
	}
	return out
}
