package recovery_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/recovery"
	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestRealInfraRepublishesWhatACrashedCoreCommitted(t *testing.T) {
	it := realInfra(t)
	room := ids.NewRoomID()
	slot := slotmap.Of(room)
	it.forgetRoomKeys(t, room)
	createRoom(t, it.store, room)
	pub := publishSetup
	pub.SubjectRoot = it.stream.SubjectRoot
	w := &world{msgs: it.store, rooms: it.store, rdb: it.rdb, pub: pub}

	const n = 120
	down := &publishtest.JetStream{}
	refuseAll(down, make(chan struct{}))
	a := w.startCore(t, "it-core-a", down)
	sendMany(t, a.router, room, n)
	a.stop()
	if wm, ok := w.watermark(room); ok && wm != 0 {
		t.Fatalf("watermark of the crashed core = %d, want it stalled at 0", wm)
	}

	b := w.startCore(t, "it-core-b", it.js)
	rooms := newRecorder(b.router)
	clk := &clock{}
	sw := newSweeper(t, recovery.Deps{Slots: owning(slot), Rooms: rooms, Msgs: it.store, Redis: it.rdb}, passSetup, clk)
	clk.advance(staleShift)
	stopSweeper := runSweeper(t, sw)
	sw.Trigger([]uint16{slot})
	rooms.awaitDone(t, room)
	stopSweeper()
	b.drain(t)

	if got, want := it.storedIDs(t), eventIDs(room, 1, n); !slices.Equal(got, want) {
		t.Fatalf("stream holds %d events, want pts 1..%d once each in order", len(got), n)
	}
	if wm, ok := w.watermark(room); !ok || wm != n {
		t.Fatalf("watermark = %d (present %v), want %d", wm, ok, n)
	}
	clk.advance(recovery.DefaultRemoveAfter + time.Minute)
	sw.Pass(t.Context(), []uint16{slot})
	if err := it.rdb.ZScore(t.Context(), publish.ActiveKey(slot), pbconv.RoomID(room)).Err(); !errors.Is(err, redis.Nil) {
		t.Fatalf("caught-up room still active after RemoveAfter: %v", err)
	}
}
