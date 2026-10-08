package worktest_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/work/worktest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func rec(seq uint64) work.Record {
	return work.Record{Kind: store.MessageInserted, Room: 7, Seq: seq, CommittedAt: time.Unix(1700000000, 0)}
}

func seqs(ds []work.Delivery) []uint64 {
	var out []uint64
	for _, d := range ds {
		out = append(out, d.Record().Seq)
	}
	return out
}

func TestFetchReturnsUpToMaxInPublishOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &worktest.Broker{}
		for s := range uint64(3) {
			b.Publish(1, rec(s+1))
		}
		q := b.Queue(1)
		first, err := q.Fetch(t.Context(), 2, time.Second)
		if err != nil || !slices.Equal(seqs(first), []uint64{1, 2}) {
			t.Fatalf("first Fetch = %v, %v; want [1 2]", seqs(first), err)
		}
		second, err := q.Fetch(t.Context(), 2, time.Second)
		if err != nil || !slices.Equal(seqs(second), []uint64{3}) {
			t.Fatalf("second Fetch = %v, %v; want [3]", seqs(second), err)
		}
		if got := b.Pending(1); got != 3 {
			t.Fatalf("Pending = %d, want 3 delivered but unacked", got)
		}
		if got := b.Pending(0); got != 0 {
			t.Fatalf("Pending(0) = %d, want 0", got)
		}
	})
}

func TestFetchWaitsForAPublishOrTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &worktest.Broker{}
		q := b.Queue(0)
		begin := time.Now()
		go func() {
			time.Sleep(300 * time.Millisecond)
			b.Publish(0, rec(1))
		}()
		got, err := q.Fetch(t.Context(), 1, time.Second)
		if err != nil || !slices.Equal(seqs(got), []uint64{1}) || time.Since(begin) != 300*time.Millisecond {
			t.Fatalf("Fetch = %v, %v after %v; want [1] after 300ms", seqs(got), err, time.Since(begin))
		}
		begin = time.Now()
		got, err = q.Fetch(t.Context(), 1, time.Second)
		if err != nil || len(got) != 0 || time.Since(begin) != time.Second {
			t.Fatalf("idle Fetch = %v, %v after %v; want nothing after 1s", seqs(got), err, time.Since(begin))
		}
	})
}

func TestAckRemovesAndNakRedeliversAfterTheDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &worktest.Broker{}
		b.Publish(0, rec(1))
		b.Publish(0, rec(2))
		q := b.Queue(0)
		ds, err := q.Fetch(t.Context(), 10, time.Second)
		if err != nil || len(ds) != 2 {
			t.Fatalf("Fetch = %v, %v", seqs(ds), err)
		}
		if err := ds[0].Ack(); err != nil {
			t.Fatalf("Ack: %v", err)
		}
		nakAt := time.Now()
		if err := ds[1].Nak(5 * time.Second); err != nil {
			t.Fatalf("Nak: %v", err)
		}
		if err := ds[0].Ack(); !errors.Is(err, worktest.ErrSettled) {
			t.Fatalf("second Ack = %v, want ErrSettled", err)
		}
		if a, n := b.Acked(), b.Naked(); len(a) != 1 || a[0].Seq != 1 || len(n) != 1 || n[0].Seq != 2 || b.Pending(0) != 1 {
			t.Fatalf("acked %v, naked %v, pending %d; want [1], [2], 1", a, n, b.Pending(0))
		}
		if early, err := q.Fetch(t.Context(), 10, time.Second); err != nil || len(early) != 0 {
			t.Fatalf("Fetch before the nak delay = %v, %v; want nothing", seqs(early), err)
		}
		again, err := q.Fetch(t.Context(), 10, time.Minute)
		if err != nil || !slices.Equal(seqs(again), []uint64{2}) || time.Since(nakAt) != 5*time.Second {
			t.Fatalf("redelivery = %v, %v after %v; want [2] exactly 5s after the nak", seqs(again), err, time.Since(nakAt))
		}
	})
}

func TestFetchStopsWhenTheContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &worktest.Broker{}
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()
		if _, err := b.Queue(0).Fetch(ctx, 1, time.Minute); !errors.Is(err, context.Canceled) {
			t.Fatalf("Fetch after cancel = %v, want context.Canceled", err)
		}
	})
}
