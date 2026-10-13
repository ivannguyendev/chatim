package roomcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

type fetchCall struct {
	room, after uint64
}

type fakeFetch struct {
	mu     sync.Mutex
	calls  []fetchCall
	gate   chan struct{}
	err    error
	result func(after uint64) Tail
}

func (f *fakeFetch) fetch(ctx context.Context, room, after uint64) (Tail, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fetchCall{room, after})
	f.mu.Unlock()
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return Tail{}, ctx.Err()
		}
	}
	if f.err != nil {
		return Tail{}, f.err
	}
	return f.result(after), nil
}

func (f *fakeFetch) made() []fetchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fetchCall(nil), f.calls...)
}

func TestLoadOfAFreshTailCallsNoFetch(t *testing.T) {
	c := newTails(t, 1<<20)
	c.Replace(777, tailOf(1, 44))
	f := &fakeFetch{}
	got, err := c.Load(t.Context(), activity(777, 44, editedAt), f.fetch)
	if err != nil || got.LastSeq != 44 || len(f.made()) != 0 {
		t.Errorf("Load = %d, %v with %d fetches; want 44, nil, 0", got.LastSeq, err, len(f.made()))
	}
}

func TestLoadExtendsFromTheCachedLastSeq(t *testing.T) {
	c := newTails(t, 1<<20)
	c.Replace(777, tailOf(1, 44))
	f := &fakeFetch{result: func(after uint64) Tail { return tailOf(after+1, 45) }}
	got, err := c.Load(t.Context(), activity(777, 45, editedAt), f.fetch)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if calls := f.made(); len(calls) != 1 || calls[0] != (fetchCall{777, 44}) {
		t.Errorf("fetches = %v, want one after seq 44", calls)
	}
	expectSeqs(t, "window", windowSeqs(got), 45, 1)
	if _, state := c.Lookup(activity(777, 45, editedAt)); state != TailFresh {
		t.Error("extended tail not cached")
	}
}

func TestLoadReloadsAfterAnEditAndStampsTheActivity(t *testing.T) {
	c := newTails(t, 1<<20)
	c.Replace(777, tailOf(1, 44))
	f := &fakeFetch{result: func(uint64) Tail { return Tail{Window: tailOf(1, 44).Window} }}
	got, err := c.Load(t.Context(), activity(777, 44, editedNow), f.fetch)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if calls := f.made(); len(calls) != 1 || calls[0].after != 0 {
		t.Errorf("fetches = %v, want one full reload", calls)
	}
	if got.LastSeq != 44 || !got.LastEditAt.Equal(editedNow) {
		t.Errorf("LastSeq/LastEditAt = %d/%v, want 44/%v", got.LastSeq, got.LastEditAt, editedNow)
	}
}

func TestConcurrentLoadsOfOneRoomShareOneFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTails(t, 1<<20)
		f := &fakeFetch{gate: make(chan struct{}), result: func(uint64) Tail { return tailOf(1, 45) }}
		var wg sync.WaitGroup
		var good atomic.Int32
		for range 20 {
			wg.Go(func() {
				if got, err := c.Load(t.Context(), activity(777, 45, editedAt), f.fetch); err == nil && got.LastSeq == 45 {
					good.Add(1)
				}
			})
		}
		synctest.Wait()
		close(f.gate)
		wg.Wait()
		if n := len(f.made()); n != 1 {
			t.Errorf("%d fetches for 20 concurrent loads, want 1", n)
		}
		if good.Load() != 20 {
			t.Errorf("%d loads got the tail, want 20", good.Load())
		}
	})
}

func TestALoadErrorIsSharedAndNotCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTails(t, 1<<20)
		boom := errors.New("scan failed")
		f := &fakeFetch{gate: make(chan struct{}), err: boom}
		var wg sync.WaitGroup
		var failed atomic.Int32
		for range 5 {
			wg.Go(func() {
				if _, err := c.Load(t.Context(), activity(777, 45, editedAt), f.fetch); errors.Is(err, boom) {
					failed.Add(1)
				}
			})
		}
		synctest.Wait()
		close(f.gate)
		wg.Wait()
		if failed.Load() != 5 || len(f.made()) != 1 {
			t.Fatalf("%d failed loads with %d fetches, want 5 and 1", failed.Load(), len(f.made()))
		}
		f.err, f.result = nil, func(uint64) Tail { return tailOf(1, 45) }
		if got, err := c.Load(t.Context(), activity(777, 45, editedAt), f.fetch); err != nil || got.LastSeq != 45 {
			t.Errorf("Load after an error = %d, %v; want a new fetch", got.LastSeq, err)
		}
		if n := len(f.made()); n != 2 {
			t.Errorf("%d fetches, want 2", n)
		}
	})
}
