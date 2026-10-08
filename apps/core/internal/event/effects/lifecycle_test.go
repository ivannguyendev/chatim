package effects_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestCloseReturnsRecordsStillWaitingForTheirDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		rg.broker.Publish(0, msg(1, 1, time.Now()))
		synctest.Wait()
		begin := time.Now()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rg.Close(stop); err != nil {
			t.Fatalf("Close = %v", err)
		}
		if took := time.Since(begin); took != 0 {
			t.Fatalf("Close took %v, want it not to wait for the delay", took)
		}
		if calls, _ := rg.msgs.record(); len(calls) != 0 {
			t.Fatalf("effect ran %v after Close, want never", calls)
		}
		if n := rg.broker.Naked(); len(n) != 1 || rg.broker.Pending(0) != 1 {
			t.Fatalf("naked %v, pending %d; want the record handed back", n, rg.broker.Pending(0))
		}
		if s := rg.Stats(); s.Failed != 0 {
			t.Fatalf("stats = %+v, want no failure for a record handed back on Close", s)
		}
	})
}

func TestCloseLetsARunningEffectFinish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		block := make(chan struct{})
		rg := newRig(t, func(rg *rig, d *effects.Deps) {
			rg.rooms.block = block
			d.Registry = effects.Registry{store.RoomInserted: {rg.rooms.effect(0)}}
		}).start(t)
		rg.broker.Publish(0, roomRec(5, time.Now()))
		synctest.Wait()
		closed := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closed <- rg.Close(ctx)
		}()
		synctest.Wait()
		select {
		case err := <-closed:
			t.Fatalf("Close returned %v while an effect was running", err)
		default:
		}
		close(block)
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if a := rg.broker.Acked(); len(a) != 1 || a[0].Room != 5 {
			t.Fatalf("acked = %v, want room 5 acked after the effect finished", a)
		}
	})
}

func TestCloseStopsAnIdleFetchAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		begin := time.Now()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rg.Close(stop); err != nil || time.Since(begin) != 0 {
			t.Fatalf("Close = %v after %v, want nil without waiting for the fetch wait", err, time.Since(begin))
		}
	})
}
