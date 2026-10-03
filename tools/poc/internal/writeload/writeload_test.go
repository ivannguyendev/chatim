package writeload

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunDeliversEveryMessageOnceWithGapFreeSeqs(t *testing.T) {
	cfg := Config{Rate: 4000, Duration: 500 * time.Millisecond, Window: 2 * time.Millisecond, MaxBatch: 64, Flushers: 3, Rooms: []uint64{1, 2, 3, 4, 5}}
	var mu sync.Mutex
	perRoom := map[uint64][]uint64{}
	biggest := 0
	res := Run(context.Background(), cfg, func(_ context.Context, batch []Msg) error {
		mu.Lock()
		defer mu.Unlock()
		biggest = max(biggest, len(batch))
		for _, m := range batch {
			perRoom[m.Room] = append(perRoom[m.Room], m.Seq)
		}
		return nil
	})
	total := 0
	for room, seqs := range perRoom {
		slices.Sort(seqs)
		for i, s := range seqs {
			if s != uint64(i+1) {
				t.Fatalf("room %d seqs are not 1..n without gaps: %v", room, seqs)
			}
		}
		total += len(seqs)
	}
	if int64(total) != res.Docs || res.Failed != 0 || res.Ack.Count != total {
		t.Fatalf("delivered %d, result docs=%d failed=%d ack=%d", total, res.Docs, res.Failed, res.Ack.Count)
	}
	if total < 1200 || total > 2100 {
		t.Fatalf("generated %d messages, want about 2000", total)
	}
	if biggest > 64 {
		t.Fatalf("batch of %d exceeds MaxBatch 64", biggest)
	}
}

func TestRunCountsFailedBatchesWithoutTimingThem(t *testing.T) {
	cfg := Config{Rate: 2000, Duration: 300 * time.Millisecond, Window: time.Millisecond, MaxBatch: 16, Flushers: 2, Rooms: []uint64{9}}
	var calls, seen atomic.Int64
	res := Run(context.Background(), cfg, func(_ context.Context, batch []Msg) error {
		seen.Add(int64(len(batch)))
		if calls.Add(1)%2 == 0 {
			return errors.New("boom")
		}
		return nil
	})
	if res.Failed == 0 || res.Docs+res.Failed != seen.Load() {
		t.Fatalf("docs=%d failed=%d, insert saw %d", res.Docs, res.Failed, seen.Load())
	}
	if int64(res.Ack.Count) != res.Docs || int64(res.Insert.Count) != res.Batches {
		t.Fatalf("ack samples=%d docs=%d, insert samples=%d batches=%d", res.Ack.Count, res.Docs, res.Insert.Count, res.Batches)
	}
}

func TestValidateRejectsNonPositiveSettings(t *testing.T) {
	ok := Config{Rate: 1, Duration: time.Second, Window: time.Millisecond, MaxBatch: 1, Flushers: 1, Rooms: []uint64{1}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []func(*Config){
		func(c *Config) { c.Rate = 0 },
		func(c *Config) { c.Duration = 0 },
		func(c *Config) { c.Window = 0 },
		func(c *Config) { c.MaxBatch = 0 },
		func(c *Config) { c.Flushers = 0 },
		func(c *Config) { c.Rooms = nil },
	}
	for i, mutate := range bad {
		c := ok
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: invalid config accepted: %+v", i, c)
		}
	}
}
