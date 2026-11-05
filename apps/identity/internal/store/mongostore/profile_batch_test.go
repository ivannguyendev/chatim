package mongostore

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	duplicateKeyCode       = 11000
	documentValidationCode = 121
)

type fakeInserts struct {
	mu      sync.Mutex
	batches [][]string
	reply   func(ctx context.Context, ids []string) error
}

func (f *fakeInserts) insert(ctx context.Context, docs []any) error {
	ids := make([]string, len(docs))
	for i, d := range docs {
		for _, e := range d.(bson.D) {
			if e.Key == "_id" {
				ids[i] = e.Value.(string)
			}
		}
	}
	f.mu.Lock()
	f.batches = append(f.batches, ids)
	f.mu.Unlock()
	if f.reply == nil {
		return nil
	}
	return f.reply(ctx, ids)
}

func (f *fakeInserts) sizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.batches))
	for i, b := range f.batches {
		out[i] = len(b)
	}
	return out
}

type writeOutcome struct {
	id      string
	written bool
	err     error
}

func testJITConfig() jitConfig {
	return jitConfig{writers: 1, window: 2 * time.Millisecond, maxBatch: 4, queue: 16, timeout: time.Second}
}

func newTestBatcher(t *testing.T, cfg jitConfig, f *fakeInserts) *jitBatcher {
	t.Helper()
	b := newJITBatcher(cfg, f.insert, func(err error) error { return errors.Join(apperr.ErrUnavailable, err) })
	t.Cleanup(b.close)
	return b
}

func startWrite(ctx context.Context, b *jitBatcher, id string, out chan<- writeOutcome) {
	go func() {
		written, err := b.write(ctx, id, bson.D{{Key: "_id", Value: id}})
		out <- writeOutcome{id: id, written: written, err: err}
	}()
}

func collectOutcomes(t *testing.T, out <-chan writeOutcome, n int) map[string]writeOutcome {
	t.Helper()
	got := make(map[string]writeOutcome, n)
	for range n {
		o := <-out
		got[o.id] = o
	}
	return got
}

func TestJITBatchFillsToMaxBatchInOneInsert(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		f := &fakeInserts{}
		cfg := testJITConfig()
		cfg.window = time.Hour
		b := newTestBatcher(t, cfg, f)
		start := time.Now()
		out := make(chan writeOutcome, 4)
		for _, id := range []string{"t│a", "t│b", "t│c", "t│d"} {
			startWrite(t.Context(), b, id, out)
		}
		for id, o := range collectOutcomes(t, out, 4) {
			if !o.written || o.err != nil {
				t.Fatalf("write %s = %v, %v; want written", id, o.written, o.err)
			}
		}
		if got := f.sizes(); !slices.Equal(got, []int{4}) {
			t.Fatalf("insert sizes = %v; want one insert of 4", got)
		}
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("full batch waited %v; want no wait for the window", waited)
		}
		if h := b.sizes.read(); h.Count != 1 || h.Sum != 4 {
			t.Fatalf("batch docs histogram count %d sum %v; want 1 and 4", h.Count, h.Sum)
		}
	})
}

func TestJITBatchInsertsAPartialBatchWhenTheWindowEnds(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		f := &fakeInserts{}
		cfg := testJITConfig()
		b := newTestBatcher(t, cfg, f)
		start := time.Now()
		out := make(chan writeOutcome, 2)
		startWrite(t.Context(), b, "t│a", out)
		startWrite(t.Context(), b, "t│b", out)
		for id, o := range collectOutcomes(t, out, 2) {
			if !o.written || o.err != nil {
				t.Fatalf("write %s = %v, %v; want written", id, o.written, o.err)
			}
		}
		if got := f.sizes(); !slices.Equal(got, []int{2}) {
			t.Fatalf("insert sizes = %v; want one insert of 2", got)
		}
		if waited := time.Since(start); waited != cfg.window {
			t.Fatalf("partial batch waited %v; want the window %v", waited, cfg.window)
		}
	})
}

func TestJITBatchSendsTheSameIDToOneWriter(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		f := &fakeInserts{reply: func(_ context.Context, ids []string) error {
			return mongo.BulkWriteException{WriteErrors: []mongo.BulkWriteError{{WriteError: mongo.WriteError{Index: 1, Code: duplicateKeyCode}}}}
		}}
		cfg := testJITConfig()
		cfg.writers = 4
		b := newTestBatcher(t, cfg, f)
		out := make(chan writeOutcome, 2)
		startWrite(t.Context(), b, "t│same", out)
		startWrite(t.Context(), b, "t│same", out)
		written := 0
		for range 2 {
			o := <-out
			if o.err != nil {
				t.Fatalf("write = %v; want nil", o.err)
			}
			if o.written {
				written++
			}
		}
		if got := f.sizes(); written != 1 || !slices.Equal(got, []int{2}) {
			t.Fatalf("written %d, insert sizes %v; want 1 written from one insert of 2", written, got)
		}
	})
}

func TestJITBatchMapsEachDocumentResult(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		f := &fakeInserts{reply: func(_ context.Context, ids []string) error {
			var bwe mongo.BulkWriteException
			for i, id := range ids {
				switch id {
				case "t│dup":
					bwe.WriteErrors = append(bwe.WriteErrors, mongo.BulkWriteError{WriteError: mongo.WriteError{Index: i, Code: duplicateKeyCode}})
				case "t│bad":
					bwe.WriteErrors = append(bwe.WriteErrors, mongo.BulkWriteError{WriteError: mongo.WriteError{Index: i, Code: documentValidationCode}})
				}
			}
			return bwe
		}}
		b := newTestBatcher(t, testJITConfig(), f)
		out := make(chan writeOutcome, 3)
		for _, id := range []string{"t│ok", "t│dup", "t│bad"} {
			startWrite(t.Context(), b, id, out)
		}
		got := collectOutcomes(t, out, 3)
		if o := got["t│ok"]; !o.written || o.err != nil {
			t.Fatalf("ok doc = %v, %v; want written", o.written, o.err)
		}
		if o := got["t│dup"]; o.written || o.err != nil {
			t.Fatalf("duplicate doc = %v, %v; want not written and nil", o.written, o.err)
		}
		if o := got["t│bad"]; o.written || !errors.Is(o.err, apperr.ErrUnavailable) {
			t.Fatalf("rejected doc = %v, %v; want unavailable", o.written, o.err)
		}
	})
}

func TestJITBatchFailsEveryDocumentOnACommandError(t *testing.T) {
	cases := map[string]func(ctx context.Context, ids []string) error{
		"network": func(context.Context, []string) error { return errors.New("connection reset") },
		"timeout": func(ctx context.Context, _ []string) error { <-ctx.Done(); return ctx.Err() },
		"write concern": func(context.Context, []string) error {
			return mongo.BulkWriteException{WriteConcernError: &mongo.WriteConcernError{Code: 64}}
		},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
			synctest.Test(t, func(t *testing.T) {
				b := newTestBatcher(t, testJITConfig(), &fakeInserts{reply: reply})
				out := make(chan writeOutcome, 2)
				startWrite(t.Context(), b, "t│a", out)
				startWrite(t.Context(), b, "t│b", out)
				for id, o := range collectOutcomes(t, out, 2) {
					if o.written || !errors.Is(o.err, apperr.ErrUnavailable) {
						t.Fatalf("write %s = %v, %v; want unavailable", id, o.written, o.err)
					}
				}
			})
		})
	}
}

func TestJITBatchStopsTheInsertAtItsTimeout(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		cfg := testJITConfig()
		cfg.maxBatch = 1
		b := newTestBatcher(t, cfg, &fakeInserts{reply: func(ctx context.Context, _ []string) error { <-ctx.Done(); return ctx.Err() }})
		start := time.Now()
		_, err := b.write(t.Context(), "t│a", bson.D{{Key: "_id", Value: "t│a"}})
		if !errors.Is(err, apperr.ErrUnavailable) || time.Since(start) != cfg.timeout {
			t.Fatalf("write = %v after %v; want unavailable after %v", err, time.Since(start), cfg.timeout)
		}
	})
}
