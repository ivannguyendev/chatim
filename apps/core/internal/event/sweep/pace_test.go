package sweep_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type clock struct {
	mu  sync.Mutex
	ats []time.Time
}

func (c *clock) record(*nats.Msg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ats = append(c.ats, time.Now())
	return nil
}

func (c *clock) times() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.ats...)
}

func TestSweepNeverPublishesFasterThanTheRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const rate, docs = 10, 40
		cfg := setup
		cfg.Rate, cfg.Batch = rate, 3
		rg := newRig(t, cfg, nil)
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		for seq := uint64(1); seq <= docs; seq++ {
			rg.send(t, room, seq, old)
		}
		c := &clock{}
		rg.js.RefuseWhen(c.record)
		start := time.Now()
		rg.start()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		ats := c.times()
		if len(ats) != docs+1 {
			t.Fatalf("published %d events, want %d", len(ats), docs+1)
		}
		expectWithinBucket(t, ats, cfg.Batch, rate)
		if last := ats[len(ats)-1].Sub(start); last < time.Duration(docs+1-cfg.Batch)*time.Second/rate-time.Millisecond {
			t.Fatalf("last event at +%v; want the rate to spread the pass", last)
		}
		expectPending(t, rg, store.StateMessages, 0)
		rg.stop(t)
	})
}

func expectWithinBucket(t *testing.T, ats []time.Time, burst, rate int) {
	t.Helper()
	for i := range ats {
		for j := i; j < len(ats); j++ {
			if allowed := burst + int(ats[j].Sub(ats[i]).Seconds()*float64(rate)+1e-6); j-i+1 > allowed {
				t.Fatalf("events %d..%d published within %v; at most %d allowed", i+1, j+1, ats[j].Sub(ats[i]), allowed)
			}
		}
	}
}

func TestCloseInterruptsTheRateWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := setup
		cfg.Rate, cfg.Batch = 1, 2
		rg := newRig(t, cfg, nil)
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		for seq := uint64(1); seq <= 6; seq++ {
			rg.send(t, room, seq, old)
		}
		rg.start()
		rg.stop(t)
		if rg.Passes() != 0 {
			t.Fatalf("passes = %d, want the pass cut short", rg.Passes())
		}
		expectCheckpoint(t, rg, store.StateMessages, time.Time{})
		if err := rg.Close(context.Background()); err != nil {
			t.Fatalf("second Close = %v", err)
		}
	})
}
