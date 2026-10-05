package effects_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type badOnce struct {
	work.Queue
	served bool
}

func (b *badOnce) Fetch(ctx context.Context, limit int, wait time.Duration) ([]work.Delivery, error) {
	if !b.served {
		b.served = true
		return nil, work.BadRecordsError{Terminated: 2}
	}
	return b.Queue.Fetch(ctx, limit, wait)
}

func TestUndecodableRecordsCountAsFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, func(rg *rig, d *effects.Deps) {
			d.Queue = func(p int) work.Queue { return &badOnce{Queue: rg.broker.Queue(p)} }
		}).start(t)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.Stats().Failed; got != 2 {
			t.Fatalf("failed = %d, want the 2 terminated records", got)
		}
		if got := rg.sink.Count(fetchFailedMsg); got != 1 {
			t.Fatalf("%q logged %d times, want 1", fetchFailedMsg, got)
		}
	})
}

func TestNewRejectsBadConfig(t *testing.T) {
	ok := effects.Registry{store.MessageInserted: {{Name: "x", Run: func(context.Context, []work.Record) []error { return nil }}}}
	queue := func(int) work.Queue { return nil }
	tests := []struct {
		name string
		deps effects.Deps
		cfg  effects.Config
	}{
		{"partitions above slot count", effects.Deps{Queue: queue, Owner: &owner{}, Registry: ok}, effects.Config{Partitions: 1025}},
		{"fetch batch above max ack pending", effects.Deps{Queue: queue, Owner: &owner{}, Registry: ok}, effects.Config{FetchBatch: work.MaxAckPending + 1}},
		{"no queue", effects.Deps{Owner: &owner{}, Registry: ok}, setup},
		{"no owner", effects.Deps{Queue: queue, Registry: ok}, setup},
		{"effect without run", effects.Deps{Queue: queue, Owner: &owner{}, Registry: effects.Registry{store.RoomInserted: {{Name: "x"}}}}, setup},
		{"negative delay", effects.Deps{Queue: queue, Owner: &owner{}, Registry: effects.Registry{store.RoomInserted: {{Name: "x", Delay: -time.Second, Run: ok[store.MessageInserted][0].Run}}}}, setup},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := effects.New(tt.deps, tt.cfg, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("New = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
