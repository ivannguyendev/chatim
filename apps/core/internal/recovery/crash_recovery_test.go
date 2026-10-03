package recovery_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/recovery"
)

var (
	errNATSDown = errors.New("nats down")
	staleShift  = recovery.DefaultStaleAfter + 3*time.Second
)

func refuseAll(js *publishtest.JetStream, refused chan<- struct{}) {
	js.RefuseWhen(func(*nats.Msg) error {
		select {
		case refused <- struct{}{}:
		default:
		}
		return errNATSDown
	})
}

func TestCommittedMessagesOfACrashedCoreArePublishedOnceInPtsOrderByTheNextOwner(t *testing.T) {
	for name, forget := range map[string]bool{"watermark left by the crashed core": false, "watermark never written": true} {
		t.Run(name, func(t *testing.T) {
			const n = 250
			w := newWorld(t)
			down := &publishtest.JetStream{}
			refuseAll(down, make(chan struct{}))
			a := w.startCore(t, "core-a", down)
			sendMany(t, a.router, roomA, n)
			a.stop()
			if wm, ok := w.watermark(roomA); ok && wm != 0 {
				t.Fatalf("watermark of the crashed core = %d, want it stalled at 0", wm)
			}
			if forget {
				w.mr.Del(publish.WatermarkKey(roomA))
			}

			up := &publishtest.JetStream{}
			b := w.startCore(t, "core-b", up)
			rooms := newRecorder(b.router)
			clk := &clock{}
			sw := newSweeper(t, recovery.Deps{Slots: owning(slotA), Rooms: rooms, Msgs: w.msgs, Redis: w.rdb}, passSetup, clk)
			clk.advance(staleShift)
			stopSweeper := runSweeper(t, sw)
			sw.Trigger([]uint16{slotA})
			if c := rooms.awaitDone(t, roomA); c.from != 0 {
				t.Fatalf("recovered from pts %d, want 0", c.from)
			}
			stopSweeper()
			b.drain(t)

			want := eventIDs(roomA, 1, n)
			if got := idsOf(up.Attempts()); !slices.Equal(got, want) {
				t.Fatalf("core B attempted %d publishes, want pts 1..%d once each in order", len(got), n)
			}
			if got := idsOf(down.Stored()); len(got) != 0 {
				t.Fatalf("crashed core stored %d events", len(got))
			}
			if wm, ok := w.watermark(roomA); !ok || wm != n {
				t.Fatalf("watermark = %d (present %v), want %d", wm, ok, n)
			}
			sw.Pass(t.Context(), []uint16{slotA})
			if !isActive(w.mr, roomA) {
				t.Fatal("room removed while its mark was younger than RemoveAfter")
			}
			clk.advance(recovery.DefaultRemoveAfter + time.Minute)
			sw.Pass(t.Context(), []uint16{slotA})
			if isActive(w.mr, roomA) {
				t.Fatal("caught-up room still active after RemoveAfter")
			}
		})
	}
}

func TestPublishesLostToANATSOutageAreRepublishedByTheNextPeriodicPass(t *testing.T) {
	const n = 40
	w := newWorld(t)
	js := &publishtest.JetStream{}
	refused := make(chan struct{}, 2*n)
	refuseAll(js, refused)
	c := w.startCore(t, "core-a", js)
	sendMany(t, c.router, roomA, n)
	for range n {
		awaitSignal(t, "refused publish", refused)
	}
	js.RefuseWhen(nil)

	rooms := newRecorder(c.router)
	cfg := passSetup
	cfg.Interval = 20 * time.Millisecond
	clk := &clock{}
	clk.advance(staleShift)
	sw := newSweeper(t, recovery.Deps{Slots: owning(slotA), Rooms: rooms, Msgs: w.msgs, Redis: w.rdb}, cfg, clk)
	stopSweeper := runSweeper(t, sw)
	rooms.awaitDone(t, roomA)
	stopSweeper()
	c.drain(t)

	if got, want := idsOf(js.Stored()), eventIDs(roomA, 1, n); !slices.Equal(got, want) {
		t.Fatalf("stored %v, want pts 1..%d in order", got, n)
	}
	if wm, ok := w.watermark(roomA); !ok || wm != n {
		t.Fatalf("watermark = %d (present %v), want %d", wm, ok, n)
	}
}

func TestRecoveryPublishesAroundAnOldHoleAndTheWatermarkPassesIt(t *testing.T) {
	w := newWorld(t)
	seed(t, w.msgs, roomA, time.Now().Add(-time.Minute), 1, 2, 4, 5)
	mark(t, w.mr, roomA, time.Now())
	js := &publishtest.JetStream{}
	c := w.startCore(t, "core-b", js)
	sw := newSweeper(t, recovery.Deps{Slots: owning(slotA), Rooms: c.router, Msgs: w.msgs, Redis: w.rdb}, passSetup, nil)
	sw.Pass(t.Context(), []uint16{slotA})
	c.drain(t)

	if got, want := idsOf(js.Stored()), []string{"101-1", "101-2", "101-4", "101-5"}; !slices.Equal(got, want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
	if wm, ok := w.watermark(roomA); !ok || wm != 5 {
		t.Fatalf("watermark = %d (present %v), want 5 past the hole", wm, ok)
	}
}

func TestRecoveryStopsAtAYoungHoleAndHoldsTheWatermarkBelowIt(t *testing.T) {
	w := newWorld(t)
	seed(t, w.msgs, roomA, time.Now().Add(-time.Minute), 1, 2)
	seed(t, w.msgs, roomA, time.Now(), 4)
	mark(t, w.mr, roomA, time.Now().Add(-time.Hour))
	js := &publishtest.JetStream{}
	c := w.startCore(t, "core-b", js)
	sw := newSweeper(t, recovery.Deps{Slots: owning(slotA), Rooms: c.router, Msgs: w.msgs, Redis: w.rdb}, passSetup, nil)
	sw.Pass(t.Context(), []uint16{slotA})
	c.drain(t)

	if got, want := idsOf(js.Stored()), []string{"101-1", "101-2"}; !slices.Equal(got, want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
	if wm, ok := w.watermark(roomA); !ok || wm != 2 {
		t.Fatalf("watermark = %d (present %v), want 2 below the hole", wm, ok)
	}
	if !isActive(w.mr, roomA) {
		t.Fatal("room behind a young hole removed")
	}
}
