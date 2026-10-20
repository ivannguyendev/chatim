package roomgroups

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

var (
	quiet   = slog.New(slog.DiscardHandler)
	errDown = errors.New("store down")
)

type fakeBuild struct {
	mu     sync.Mutex
	groups map[uint64][]string
	err    error
	gate   chan struct{}
	calls  int
}

func (f *fakeBuild) build(ctx context.Context, _ string, room uint64) (map[string]struct{}, error) {
	f.mu.Lock()
	f.calls++
	gate, err, ids := f.gate, f.err, f.groups[room]
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}

func (f *fakeBuild) set(room uint64, groups ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups[room] = groups
}

func (f *fakeBuild) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeBuild) hold() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gate = make(chan struct{})
	return f.gate
}

func (f *fakeBuild) release(gate chan struct{}) {
	f.mu.Lock()
	f.gate = nil
	f.mu.Unlock()
	close(gate)
}

func (f *fakeBuild) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func idsOf(s Set) []string { return slices.Sorted(maps.Keys(s.Groups)) }
