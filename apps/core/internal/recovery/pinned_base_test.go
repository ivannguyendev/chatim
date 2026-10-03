package recovery_test

import (
	"context"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/recovery"
)

func TestNextOwnerWritingFirstCannotHideWhatACrashedCoreCommitted(t *testing.T) {
	const n = 5
	w := newWorld(t)
	a := w.startCore(t, "core-a", nil)
	sendMany(t, a.router, roomA, n)
	a.stop()
	if wm, ok := w.watermark(roomA); !ok || wm != 0 {
		t.Fatalf("watermark after the crashed core = %d (present %v), want 0 pinned by its first mark", wm, ok)
	}

	js := &publishtest.JetStream{}
	b := w.startCore(t, "core-b", js)
	ack, err := b.router.Send(context.Background(), actor.SendCmd{Tenant: tenant, User: "bob", Room: roomA, CID: "after-crash", Text: "hello"})
	if err != nil || ack.Pts != n+1 {
		t.Fatalf("send on the next owner = %+v, %v; want pts %d", ack, err, n+1)
	}
	b.drain(t)
	if wm, ok := w.watermark(roomA); !ok || wm != 0 {
		t.Fatalf("watermark after the next owner published pts %d = %d (present %v), want it held at 0", n+1, wm, ok)
	}
	if got := idsOf(js.Stored()); !slices.Equal(got, []string{"101-6"}) {
		t.Fatalf("stored before recovery %v, want only the next owner's write", got)
	}

	restarted := w.startCore(t, "core-b-restarted", js)
	clk := &clock{}
	clk.advance(staleShift)
	sw := newSweeper(t, recovery.Deps{Slots: owning(slotA), Rooms: restarted.router, Msgs: w.msgs, Redis: w.rdb}, passSetup, clk)
	sw.Pass(t.Context(), []uint16{slotA})
	restarted.drain(t)

	want := append([]string{"101-6"}, eventIDs(roomA, 1, n)...)
	if got := idsOf(js.Stored()); !slices.Equal(got, want) {
		t.Fatalf("stored %v, want %v: pts 1..%d recovered in order after the next owner's write", got, want, n)
	}
	if wm, ok := w.watermark(roomA); !ok || wm != n+1 {
		t.Fatalf("watermark after recovery = %d (present %v), want %d", wm, ok, n+1)
	}
}
