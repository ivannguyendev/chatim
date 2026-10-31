package directory_test

import (
	"fmt"
	mrand "math/rand/v2"
	"runtime"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const benchUsers = 100000

func benchEntries(users int) []store.DirectoryEntry {
	entries := make([]store.DirectoryEntry, users)
	for i := range entries {
		id := accountID(i + 1)
		entries[i] = store.DirectoryEntry{
			TenantID: "shop", ID: id, DisplayName: fmt.Sprintf("User %d", i+1),
			AvatarURL: "https://cdn.example.com/avatars/" + id + ".png", State: store.StateActive, Ver: 1,
		}
	}
	return entries
}

func filledCache(users int) *directory.Cache {
	cache := directory.NewCache()
	cache.ApplyEntries(benchEntries(users))
	return cache
}

func randomIDBatches(batches, size, users int) [][]string {
	out := make([][]string, batches)
	for i := range out {
		out[i] = make([]string, size)
		for j := range out[i] {
			out[i][j] = accountID(1 + mrand.IntN(users))
		}
	}
	return out
}

func benchmarkLookup(b *testing.B, size int) {
	cache := filledCache(benchUsers)
	batches := randomIDBatches(1024, size, benchUsers)
	b.ReportAllocs()
	n := 0
	for b.Loop() {
		if got := cache.Lookup("shop", batches[n%len(batches)]); len(got) != size {
			b.Fatalf("found %d of %d", len(got), size)
		}
		n++
	}
}

func BenchmarkLookup50(b *testing.B) { benchmarkLookup(b, 50) }

func BenchmarkLookup500(b *testing.B) { benchmarkLookup(b, 500) }

func heapInUse() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func BenchmarkFill100K(b *testing.B) {
	var heapBytes uint64
	for b.Loop() {
		b.StopTimer()
		before := heapInUse()
		entries := benchEntries(benchUsers)
		b.StartTimer()
		cache := directory.NewCache()
		cache.ApplyEntries(entries)
		b.StopTimer()
		heapBytes = heapInUse() - before
		if cache.Len() != benchUsers {
			b.Fatalf("cache holds %d users; want %d", cache.Len(), benchUsers)
		}
		runtime.KeepAlive(cache)
		b.StartTimer()
	}
	b.ReportMetric(float64(heapBytes)/(1<<20), "heap-MB")
}
