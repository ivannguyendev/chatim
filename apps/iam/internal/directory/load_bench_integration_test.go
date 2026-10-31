package directory_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
)

const (
	loadAccounts  = 100000
	loadReplicas  = 10
	loadSlots     = 4
	loadRunBudget = 2 * time.Minute
)

type slotCounter struct {
	store.DirectoryReader
	holders *atomic.Int64
	peak    *atomic.Int64
}

func (s slotCounter) TryLoadSlot(ctx context.Context, slots int) (func(), bool, error) {
	release, ok, err := s.DirectoryReader.TryLoadSlot(ctx, slots)
	if err != nil || !ok {
		return release, ok, err
	}
	raisePeak(s.peak, s.holders.Add(1))
	return func() {
		s.holders.Add(-1)
		release()
	}, true, nil
}

func raisePeak(peak *atomic.Int64, held int64) {
	for {
		seen := peak.Load()
		if held <= seen || peak.CompareAndSwap(seen, held) {
			return
		}
	}
}

func runLoader(tb testing.TB, reader store.DirectoryReader, revocations directory.RevocationSource) *directory.Cache {
	tb.Helper()
	list, err := iamsync.NewList(0)
	if err != nil {
		tb.Fatalf("NewList: %v", err)
	}
	cache := directory.NewCache()
	loader := &directory.Loader{Reader: reader, Revocations: revocations, Cache: cache, List: list, Slots: loadSlots, Stats: directory.NewStats(cache)}
	ctx, cancel := context.WithTimeout(context.Background(), loadRunBudget)
	defer cancel()
	if err := loader.Run(ctx); err != nil {
		tb.Errorf("Loader.Run: %v", err)
		return cache
	}
	if !cache.Ready() || cache.Len() != loadAccounts {
		tb.Errorf("ready=%v cache=%d; want ready with %d users", cache.Ready(), cache.Len(), loadAccounts)
	}
	return cache
}

func BenchmarkLoad100K(b *testing.B) {
	s, raw, _ := itestDatabase(b)
	seedDirectory(b, raw, loadAccounts)
	for b.Loop() {
		runLoader(b, s, s)
	}
	b.ReportMetric(b.Elapsed().Seconds()/float64(b.N), "s/load")
}

func TestTenLoadersFinish(t *testing.T) {
	_, raw, uri := itestDatabase(t)
	seedDirectory(t, raw, loadAccounts)
	var holders, peak atomic.Int64
	readyAfter := make([]time.Duration, loadReplicas)
	stores := make([]*pgstore.Store, loadReplicas)
	for i := range stores {
		s, err := pgstore.Open(context.Background(), uri, pgstore.Options{MaxConns: 4, RevocationKeep: 16 * time.Minute})
		if err != nil {
			t.Fatalf("Open replica %d: %v", i, err)
		}
		t.Cleanup(s.Close)
		stores[i] = s
	}
	began := time.Now()
	var wg sync.WaitGroup
	for i, s := range stores {
		wg.Go(func() {
			runLoader(t, slotCounter{DirectoryReader: s, holders: &holders, peak: &peak}, s)
			readyAfter[i] = time.Since(began)
		})
	}
	wg.Wait()
	total := time.Since(began)
	if got := peak.Load(); got > loadSlots {
		t.Fatalf("peak slot holders = %d; want at most %d", got, loadSlots)
	}
	slices.Sort(readyAfter)
	t.Logf("replicas=%d slots=%d all ready after %.3fs, peak slot holders=%d, ready times=%v", loadReplicas, loadSlots, total.Seconds(), peak.Load(), readyAfter)
}
