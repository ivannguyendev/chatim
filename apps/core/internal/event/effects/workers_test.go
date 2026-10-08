package effects_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestFetchesOnlyPartitionsItOwns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		due := time.Now().Add(-delay)
		rg.broker.Publish(0, msg(1, 1, due))
		rg.broker.Publish(1, msg(2, 1, due))
		time.Sleep(tick)
		synctest.Wait()
		if got := rooms(rg.broker.Acked()); !slices.Equal(got, []uint64{1}) || rg.broker.Pending(1) != 1 {
			t.Fatalf("acked rooms %v, pending on partition 1 = %d; want only room 1 and partition 1 untouched", got, rg.broker.Pending(1))
		}
		rg.owner.set(1, true)
		time.Sleep(setup.Poll + tick)
		synctest.Wait()
		if got := rooms(rg.broker.Acked()); !slices.Equal(got, []uint64{1, 2}) || rg.broker.Pending(1) != 0 {
			t.Fatalf("acked rooms %v after taking slot 1, want [1 2]", got)
		}
	})
}

func TestRunsAnEffectOnlyAfterItsDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		committed := time.Now()
		rg.broker.Publish(0, msg(1, 1, committed))
		time.Sleep(delay - time.Millisecond)
		synctest.Wait()
		if calls, _ := rg.msgs.record(); len(calls) != 0 {
			t.Fatalf("calls before the delay = %v, want none", calls)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		calls, at := rg.msgs.record()
		if len(calls) != 1 || !at[0].Equal(committed.Add(delay)) {
			t.Fatalf("calls %v at %v, want one exactly at commit + %v", calls, at, delay)
		}
		if got := rg.broker.Acked(); len(got) != 1 {
			t.Fatalf("acked = %v, want the record", got)
		}
	})
}

func TestGroupsABatchByKind(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		due := time.Now().Add(-delay)
		rg.broker.Publish(0, msg(1, 1, due))
		rg.broker.Publish(0, roomRec(5, time.Now()))
		rg.broker.Publish(0, msg(1, 2, due))
		rg.start(t)
		synctest.Wait()
		msgCalls, _ := rg.msgs.record()
		roomCalls, _ := rg.rooms.record()
		if len(msgCalls) != 1 || !slices.Equal(seqs(msgCalls[0]), []uint64{1, 2}) {
			t.Fatalf("msg effect calls = %v, want one call with seq 1 and 2", msgCalls)
		}
		if len(roomCalls) != 1 || !slices.Equal(rooms(roomCalls[0]), []uint64{5}) {
			t.Fatalf("room effect calls = %v, want one call with room 5", roomCalls)
		}
		if got := len(rg.broker.Acked()); got != 3 {
			t.Fatalf("acked %d records, want 3", got)
		}
	})
}

func TestKeepsPublishOrderWithinAPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		due := time.Now().Add(-delay)
		for s := uint64(1); s <= 10; s++ {
			rg.broker.Publish(0, msg(1, s, due))
		}
		rg.start(t)
		time.Sleep(tick)
		synctest.Wait()
		calls, _ := rg.msgs.record()
		var got []uint64
		for _, c := range calls {
			if len(c) > setup.FetchBatch {
				t.Fatalf("a call carried %d records, want at most %d", len(c), setup.FetchBatch)
			}
			got = append(got, seqs(c)...)
		}
		if !slices.Equal(got, []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) || len(calls) != 2 {
			t.Fatalf("effect saw %v in %d calls, want 1..10 in order in 2 batches", got, len(calls))
		}
	})
}

func TestAcksSucceededRecordsAndRetriesFailedOnes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		rg.msgs.failSeqs(2)
		due := time.Now().Add(-delay)
		for s := uint64(1); s <= 3; s++ {
			rg.broker.Publish(0, msg(1, s, due))
		}
		rg.start(t)
		synctest.Wait()
		if a, n := seqs(rg.broker.Acked()), seqs(rg.broker.Naked()); !slices.Equal(a, []uint64{1, 3}) || !slices.Equal(n, []uint64{2}) {
			t.Fatalf("acked %v, naked %v; want [1 3] and [2]", a, n)
		}
		if s := rg.Stats(); s.Processed != 2 || s.Failed != 1 {
			t.Fatalf("stats = %+v, want 2 processed, 1 failed", s)
		}
		rg.msgs.failSeqs()
		time.Sleep(retry)
		synctest.Wait()
		calls, _ := rg.msgs.record()
		if a := seqs(rg.broker.Acked()); !slices.Equal(a, []uint64{1, 3, 2}) || len(calls) != 2 || !slices.Equal(seqs(calls[1]), []uint64{2}) {
			t.Fatalf("acked %v, calls %v; want seq 2 retried alone after %v", a, calls, retry)
		}
		if s := rg.Stats(); s.Processed != 3 || s.Failed != 1 {
			t.Fatalf("stats = %+v, want 3 processed, 1 failed", s)
		}
	})
}

func TestAcksOnlyWhenEveryEffectOfTheKindSucceeded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		order := &journal{}
		rg := newRig(t, func(_ *rig, d *effects.Deps) {
			first := &recorder{name: "first", journal: order}
			second := &recorder{name: "second", journal: order, fail: map[uint64]bool{1: true}}
			d.Registry = effects.Registry{store.MessageInserted: {first.effect(0), second.effect(0)}}
		}).start(t)
		rg.broker.Publish(0, msg(1, 1, time.Now()))
		synctest.Wait()
		if got := order.list(); !slices.Equal(got, []string{"first", "second"}) {
			t.Fatalf("effects ran as %v, want registry order", got)
		}
		if a, n := rg.broker.Acked(), rg.broker.Naked(); len(a) != 0 || len(n) != 1 {
			t.Fatalf("acked %v, naked %v; want the record naked because the second effect failed", a, n)
		}
	})
}

func TestLagShowsHowLateAnEffectRan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		rg.broker.Publish(0, msg(1, 1, time.Now().Add(-delay-3*time.Second)))
		synctest.Wait()
		if got := rg.Stats().Lag; got != 3*time.Second {
			t.Fatalf("lag = %v, want 3s past commit + delay", got)
		}
		time.Sleep(wait + tick)
		synctest.Wait()
		if got := rg.Stats().Lag; got != 0 {
			t.Fatalf("lag of an idle partition = %v, want 0", got)
		}
	})
}
