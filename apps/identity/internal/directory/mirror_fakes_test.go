package directory_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/auth"
)

const streamIdleAfter = time.Second

type fakeSlot struct {
	mu       sync.Mutex
	renews   []time.Time
	releases int
	lost     bool
}

func (s *fakeSlot) Renew(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renews = append(s.renews, time.Now())
	return !s.lost, nil
}

func (s *fakeSlot) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases++
}

func (s *fakeSlot) counts() (renews, releases int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.renews), s.releases
}

type streamResult struct {
	ev  store.StreamEvent
	has bool
	err error
}

type fakeStream struct {
	from      string
	results   chan streamResult
	idleToken store.StreamToken
	closed    atomic.Int32
	closeErr  atomic.Value
}

func (s *fakeStream) Next(ctx context.Context) (store.StreamEvent, bool, error) {
	idle := time.NewTimer(streamIdleAfter)
	defer idle.Stop()
	select {
	case r := <-s.results:
		return r.ev, r.has, r.err
	case <-idle.C:
		return store.StreamEvent{Token: s.idleToken}, false, nil
	case <-ctx.Done():
		return store.StreamEvent{}, false, ctx.Err()
	}
}

func (s *fakeStream) Close(ctx context.Context) error {
	s.closed.Add(1)
	s.closeErr.Store(fmt.Sprint(ctx.Err()))
	return nil
}

func (s *fakeStream) send(r streamResult) { s.results <- r }

type fakeSource struct {
	*fakeRevocations
	mu         sync.Mutex
	entries    []store.DirectoryEntry
	pageHook   func(call int) error
	pageCalls  int
	pageLimits []int
	slotErrs   int
	slotBusy   int
	slotCalls  []time.Time
	slotLost   int
	slots      []*fakeSlot
	starts     int
	opens      []time.Time
	openErrs   []error
	streams    []*fakeStream
}

func (f *fakeSource) set(apply func(f *fakeSource)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	apply(f)
}

func (f *fakeSource) read(get func(f *fakeSource)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	get(f)
}

func (f *fakeSource) LoadPage(_ context.Context, after store.UserKey, limit int) ([]store.DirectoryEntry, error) {
	f.mu.Lock()
	f.pageCalls++
	f.pageLimits = append(f.pageLimits, limit)
	call, hook := f.pageCalls, f.pageHook
	f.mu.Unlock()
	if hook != nil {
		if err := hook(call); err != nil {
			return nil, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var page []store.DirectoryEntry
	for _, e := range f.entries {
		if cmp.Or(cmp.Compare(e.TenantID, after.TenantID), cmp.Compare(e.ID, after.ID)) > 0 && len(page) < limit {
			page = append(page, e)
		}
	}
	return page, nil
}

func (f *fakeSource) TryLoadSlot(_ context.Context, purpose string, slots int) (store.Slot, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slotCalls = append(f.slotCalls, time.Now())
	if purpose != "directory_load" || slots != 4 {
		return nil, false, fmt.Errorf("slot %q of %d; want directory_load of 4", purpose, slots)
	}
	if f.slotErrs > 0 {
		f.slotErrs--
		return nil, false, errDown
	}
	if f.slotBusy > 0 {
		f.slotBusy--
		return nil, false, nil
	}
	slot := &fakeSlot{lost: f.slotLost > 0}
	if f.slotLost > 0 {
		f.slotLost--
	}
	f.slots = append(f.slots, slot)
	return slot, true, nil
}

func (f *fakeSource) StreamStart(context.Context) (store.StreamToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	token := store.StreamToken(fmt.Sprintf("T%d", f.starts))
	f.starts++
	return token, nil
}

func (f *fakeSource) OpenStream(_ context.Context, from store.StreamToken) (store.ChangeStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens = append(f.opens, time.Now())
	if len(f.openErrs) > 0 {
		err := f.openErrs[0]
		f.openErrs = f.openErrs[1:]
		return nil, err
	}
	s := &fakeStream{from: string(from), results: make(chan streamResult), idleToken: from}
	f.streams = append(f.streams, s)
	return s, nil
}

func (f *fakeSource) stream(t *testing.T, n int) *fakeStream {
	t.Helper()
	var s *fakeStream
	f.read(func(f *fakeSource) {
		if n < len(f.streams) {
			s = f.streams[n]
		}
	})
	if s == nil {
		t.Fatalf("stream %d not opened", n)
	}
	return s
}

func (f *fakeSource) slot(t *testing.T, n int) *fakeSlot {
	t.Helper()
	var s *fakeSlot
	f.read(func(f *fakeSource) {
		if n < len(f.slots) {
			s = f.slots[n]
		}
	})
	if s == nil {
		t.Fatalf("slot %d not taken", n)
	}
	return s
}

type mirrorRig struct {
	src      *fakeSource
	cache    *directory.Cache
	list     *auth.RevocationList
	stats    *directory.Stats
	loaded   atomic.Int32
	trusted  atomic.Int32
	recheck  time.Duration
	cancel   context.CancelFunc
	done     chan error
	stopOnce sync.Once
}

func newMirrorRig(t *testing.T, entries ...store.DirectoryEntry) *mirrorRig {
	t.Helper()
	list, err := auth.NewNodeRevocationList(0)
	if err != nil {
		t.Fatalf("NewList: %v", err)
	}
	cache := directory.NewCache()
	return &mirrorRig{
		src:   &fakeSource{fakeRevocations: &fakeRevocations{}, entries: entries},
		cache: cache, list: list, stats: directory.NewStats(cache), recheck: time.Minute,
	}
}

func (r *mirrorRig) mirror() *directory.Mirror {
	return &directory.Mirror{
		Source: r.src, Cache: r.cache, List: r.list, Stats: r.stats, Slots: 4, Recheck: r.recheck,
		OnLoaded: func() { r.loaded.Add(1) }, OnTrust: func() { r.trusted.Add(1) },
	}
}

func (r *mirrorRig) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.done = cancel, make(chan error, 1)
	m := r.mirror()
	go func() { r.done <- m.Run(ctx) }()
	t.Cleanup(func() { r.stop(t) })
}

func (r *mirrorRig) stop(t *testing.T) {
	t.Helper()
	r.stopOnce.Do(func() {
		r.cancel()
		if err := <-r.done; err != nil {
			t.Errorf("Mirror.Run = %v; want nil", err)
		}
	})
}

func manyEntries(n int) []store.DirectoryEntry {
	out := make([]store.DirectoryEntry, 0, n)
	for i := range n {
		out = append(out, entry("shop", fmt.Sprintf("u%06d", i), fmt.Sprintf("User %d", i), 1))
	}
	return out
}

func userEvent(e store.DirectoryEntry, token string) streamResult {
	return streamResult{ev: store.StreamEvent{Kinds: []store.EventKind{store.EventUser}, User: e, Token: store.StreamToken(token)}, has: true}
}

func kindEvent(token string, kinds ...store.EventKind) streamResult {
	return streamResult{ev: store.StreamEvent{Kinds: kinds, Token: store.StreamToken(token)}, has: true}
}

var errMidPage = errors.New("page read failed")
