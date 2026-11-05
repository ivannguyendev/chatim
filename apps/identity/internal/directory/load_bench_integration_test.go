package directory_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/mongostore"
)

const (
	loadUsers     = 100000
	loadReplicas  = 10
	loadSlots     = 4
	loadRunBudget = 2 * time.Minute
)

type slotCounter struct {
	*mongostore.Store
	holders *atomic.Int64
	peak    *atomic.Int64
}

func (s slotCounter) TryLoadSlot(ctx context.Context, purpose string, slots int) (store.Slot, bool, error) {
	slot, ok, err := s.Store.TryLoadSlot(ctx, purpose, slots)
	if err != nil || !ok {
		return slot, ok, err
	}
	raisePeak(s.peak, s.holders.Add(1))
	return &countedSlot{Slot: slot, holders: s.holders}, true, nil
}

type countedSlot struct {
	store.Slot
	holders *atomic.Int64
	once    sync.Once
}

func (c *countedSlot) Release() {
	c.once.Do(func() { c.holders.Add(-1) })
	c.Slot.Release()
}

func raisePeak(peak *atomic.Int64, held int64) {
	for {
		seen := peak.Load()
		if held <= seen || peak.CompareAndSwap(seen, held) {
			return
		}
	}
}

func loadOnce(tb testing.TB, src directory.Source) {
	tb.Helper()
	m := startMirror(tb, src, loadSlots)
	select {
	case <-m.loaded:
	case <-time.After(loadRunBudget):
		tb.Errorf("directory load did not finish within %v", loadRunBudget)
	}
	if err := m.stop(); err != nil {
		tb.Errorf("Mirror.Run: %v", err)
	}
	if !m.cache.Ready() || m.cache.Len() != loadUsers {
		tb.Errorf("ready=%v cache=%d; want ready with %d users", m.cache.Ready(), m.cache.Len(), loadUsers)
	}
}

func BenchmarkLoad100K(b *testing.B) {
	it := itestDatabase(b)
	seedUsers(b, it, loadUsers)
	for b.Loop() {
		loadOnce(b, it.store)
	}
	b.ReportMetric(b.Elapsed().Seconds()/float64(b.N), "s/load")
}

func TestTenLoadersFinish(t *testing.T) {
	it := itestDatabase(t)
	seedUsers(t, it, loadUsers)
	var holders, peak atomic.Int64
	readyAfter := make([]time.Duration, loadReplicas)
	stores := make([]*mongostore.Store, loadReplicas)
	for i := range stores {
		stores[i] = openStore(t, it.opts)
	}
	began := time.Now()
	var wg sync.WaitGroup
	for i, s := range stores {
		wg.Go(func() {
			loadOnce(t, slotCounter{Store: s, holders: &holders, peak: &peak})
			readyAfter[i] = time.Since(began)
		})
	}
	wg.Wait()
	total := time.Since(began)
	if got := peak.Load(); got > loadSlots || got == 0 {
		t.Fatalf("peak slot holders = %d; want 1 to %d", got, loadSlots)
	}
	slices.Sort(readyAfter)
	t.Logf("replicas=%d slots=%d all ready after %.3fs, peak slot holders=%d, ready times=%v", loadReplicas, loadSlots, total.Seconds(), peak.Load(), readyAfter)
}
