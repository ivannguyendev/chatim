package mongostore

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func blockingInserts(release <-chan struct{}) *fakeInserts {
	return &fakeInserts{reply: func(ctx context.Context, _ []string) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
}

func oneDocConfig() jitConfig {
	cfg := testJITConfig()
	cfg.maxBatch = 1
	cfg.queue = 1
	cfg.timeout = time.Hour
	return cfg
}

func TestJITBatchRefusesAtOnceWhenTheQueueIsFull(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		f := blockingInserts(release)
		b := newTestBatcher(t, oneDocConfig(), f)
		out := make(chan writeOutcome, 2)
		startWrite(t.Context(), b, "t│inflight", out)
		synctest.Wait()
		startWrite(t.Context(), b, "t│queued", out)
		synctest.Wait()
		start := time.Now()
		written, err := b.write(t.Context(), "t│refused", bson.D{{Key: "_id", Value: "t│refused"}})
		if written || !errors.Is(err, apperr.ErrUnavailable) || time.Since(start) != 0 {
			t.Fatalf("write on a full queue = %v, %v after %v; want unavailable at once", written, err, time.Since(start))
		}
		close(release)
		for id, o := range collectOutcomes(t, out, 2) {
			if !o.written || o.err != nil {
				t.Fatalf("write %s = %v, %v; want written", id, o.written, o.err)
			}
		}
		if got := f.sizes(); !slices.Equal(got, []int{1, 1}) {
			t.Fatalf("insert sizes = %v; want two inserts of 1", got)
		}
	})
}

func TestJITBatchCloseFailsWaitingDocumentsAndStopsWriters(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		f := blockingInserts(make(chan struct{}))
		b := newJITBatcher(oneDocConfig(), f.insert, func(err error) error { return errors.Join(apperr.ErrUnavailable, err) })
		out := make(chan writeOutcome, 2)
		startWrite(t.Context(), b, "t│inflight", out)
		synctest.Wait()
		startWrite(t.Context(), b, "t│queued", out)
		synctest.Wait()
		b.close()
		for id, o := range collectOutcomes(t, out, 2) {
			if o.written || !errors.Is(o.err, apperr.ErrUnavailable) {
				t.Fatalf("write %s after close = %v, %v; want unavailable", id, o.written, o.err)
			}
		}
		if got := f.sizes(); !slices.Equal(got, []int{1}) {
			t.Fatalf("insert sizes = %v; want only the in-flight insert", got)
		}
		if written, err := b.write(t.Context(), "t│late", bson.D{{Key: "_id", Value: "t│late"}}); written || !errors.Is(err, apperr.ErrUnavailable) {
			t.Fatalf("write after close = %v, %v; want unavailable", written, err)
		}
		b.close()
	})
}

func TestJITBatchReturnsTheCallerContextError(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		f := blockingInserts(release)
		b := newTestBatcher(t, oneDocConfig(), f)
		ctx, cancel := context.WithCancel(t.Context())
		out := make(chan writeOutcome, 1)
		startWrite(ctx, b, "t│a", out)
		synctest.Wait()
		cancel()
		if o := <-out; o.written || !errors.Is(o.err, context.Canceled) {
			t.Fatalf("write with a cancelled caller = %v, %v; want context.Canceled", o.written, o.err)
		}
		close(release)
		synctest.Wait()
		if got := f.sizes(); !slices.Equal(got, []int{1}) {
			t.Fatalf("insert sizes = %v; want the doc still sent once", got)
		}
		if written, err := b.write(ctx, "t│b", bson.D{{Key: "_id", Value: "t│b"}}); written || !errors.Is(err, context.Canceled) {
			t.Fatalf("write with a done caller = %v, %v; want context.Canceled", written, err)
		}
		if got := f.sizes(); len(got) != 1 {
			t.Fatalf("insert sizes = %v; want no insert for a done caller", got)
		}
	})
}
