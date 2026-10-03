package actor_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestFreshActorOfAnOldRoomPinsItsLoadedLastAsTheWatermark(t *testing.T) {
	w := newWorld(t)
	seed(t, w.msgs, roomA, time.Now().Add(-time.Hour), span(1, 50)...)
	core := startCoreWith(t, w.msgs, w.rooms, &fakeRegistry{}, &publishSpy{}, marksFor(t, w.rdb))
	if ack := mustSend(t, core, cmd(roomA, "alice", "wake")); ack.Seq != 51 {
		t.Fatalf("first send after waking assigned seq %d, want 51", ack.Seq)
	}
	key := publish.WatermarkKey(roomA)
	if v, err := w.mr.Get(key); err != nil || v != "50" {
		t.Fatalf("watermark pinned by the first mark = %q (%v), want the loaded last 50", v, err)
	}
	if ttl := w.mr.TTL(key); ttl != publish.DefaultWatermarkTTL {
		t.Fatalf("pinned watermark ttl = %v, want %v", ttl, publish.DefaultWatermarkTTL)
	}
}

func TestWakingAnOldRoomPublishesOnlyItsNewMessage(t *testing.T) {
	w := newWorld(t)
	seed(t, w.msgs, roomA, time.Now().Add(-time.Hour), span(1, 50)...)
	js := &publishtest.JetStream{}
	pub := startPublisher(t, js, w.rdb)
	core := startCoreWith(t, w.msgs, w.rooms, &fakeRegistry{}, pub, marksFor(t, w.rdb))
	mustSend(t, core, cmd(roomA, "alice", "wake"))
	drain(t, core.Close, pub.Close)
	if v, _ := w.mr.Get(publish.WatermarkKey(roomA)); v != "51" {
		t.Fatalf("watermark after drain = %q, want 51", v)
	}
	var ids []string
	for _, m := range js.Stored() {
		ids = append(ids, publishtest.MsgID(m))
	}
	if !slices.Equal(ids, []string{"101-0-51"}) {
		t.Fatalf("published %v, want only the new message", ids)
	}
}

func TestMarkFloorStaysBelowASeqThatIsStillBeingResent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := baseConfig
		cfg.GroupDeadline, cfg.ReservationTTL = 10*time.Second, 20*time.Second
		rg := started(t, cfg)
		rg.sub.then(rg.sub.insert, func(msgs []domain.Message) []store.Result {
			return append([]store.Result{{Outcome: store.Unknown}}, rg.sub.insert(msgs[1:])...)
		})
		rg.sub.hold()
		waits := []<-chan sendResult{sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "a1"))}
		synctest.Wait()
		for _, cid := range []string{"a2", "a3"} {
			waits = append(waits, sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", cid)))
			synctest.Wait()
		}
		rg.sub.release()
		synctest.Wait()
		time.Sleep(actor.ActiveMarkEvery)
		rg.sub.release()
		synctest.Wait()
		rg.sub.open()
		for i, w := range waits {
			if r := <-w; r.err != nil || r.ack.Seq != uint64(i+1) {
				t.Fatalf("send %d = %+v, %v; want seq %d", i, r.ack, r.err, i+1)
			}
		}
		if got := rg.marks.pinned(roomA); !slices.Equal(got, []uint64{0, 1}) {
			t.Fatalf("mark floors = %v, want [0 1]: the resend of seq 2 must not be covered by last 3", got)
		}
		rg.cancel()
		_ = rg.wait()
	})
}
