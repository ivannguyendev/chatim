package actor_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var holeGrace = baseConfig.ReservationTTL + time.Second

func seed(t *testing.T, m store.Messages, room uint64, at time.Time, pts ...uint64) {
	t.Helper()
	msgs := make([]domain.Message, len(pts))
	for i, p := range pts {
		msgs[i] = domain.Message{Room: room, Seq: p, Pts: p, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "seeded", CID: fmt.Sprintf("s%d", p), CreatedAt: at.UTC().Truncate(time.Millisecond)}
	}
	for i, r := range m.Insert(context.Background(), msgs) {
		if r.Outcome != store.Inserted {
			t.Fatalf("seed pts %d: %v", pts[i], r.Outcome)
		}
	}
}

func span(from, to uint64) []uint64 {
	var out []uint64
	for p := from; p <= to; p++ {
		out = append(out, p)
	}
	return out
}

func mustRecover(t *testing.T, r *actor.Router, room, from uint64) {
	t.Helper()
	if err := r.Recover(context.Background(), room, from); err != nil {
		t.Fatalf("Recover(%d, %d): %v", room, from, err)
	}
}

func handedPts(rg *rig, room uint64) []uint64 {
	var out []uint64
	for _, ev := range rg.events.events(room) {
		out = append(out, ev.GetPts())
	}
	return out
}

func expectCalls(t *testing.T, rg *rig, room uint64, want ...string) {
	t.Helper()
	if got := rg.events.calls(room); !slices.Equal(got, want) {
		t.Fatalf("publisher calls for room %d = %q, want %q", room, got, want)
	}
}

func TestRecoverRepublishesStoredMessagesAfterFromInPtsOrder(t *testing.T) {
	rg := started(t, baseConfig)
	seed(t, rg.msgs, roomA, time.Now(), span(1, 250)...)
	mustRecover(t, rg.Router, roomA, 120)
	if got := handedPts(rg, roomA); !slices.Equal(got, span(121, 250)) {
		t.Fatalf("republished pts %v, want 121..250", got)
	}
	docs := timeline(t, rg.msgs, roomA)
	for i, ev := range rg.events.events(roomA) {
		if want := pbconv.MessageCreated(domain.RoomGroup, docs[120+i]); !proto.Equal(ev, want) {
			t.Fatalf("event %d = %v, want %v", i, ev, want)
		}
	}
}

func TestRecoverHandsABoundedBatchAndTheNextRequestContinues(t *testing.T) {
	rg := started(t, baseConfig)
	seed(t, rg.msgs, roomA, time.Now(), span(1, 1100)...)
	mustRecover(t, rg.Router, roomA, 0)
	if got := handedPts(rg, roomA); !slices.Equal(got, span(1, 1000)) {
		t.Fatalf("first request handed %d pts, want 1..1000", len(got))
	}
	mustRecover(t, rg.Router, roomA, 1000)
	if got := handedPts(rg, roomA); !slices.Equal(got, span(1, 1100)) {
		t.Fatalf("after the second request handed %d pts, want 1..1100", len(got))
	}
}

func TestRecoverWithNothingAfterFromHandsNothing(t *testing.T) {
	rg := started(t, baseConfig)
	seed(t, rg.msgs, roomA, time.Now(), 1, 2)
	mustRecover(t, rg.Router, roomA, 2)
	mustRecover(t, rg.Router, roomB, 0)
	expectCalls(t, rg, roomA)
	expectCalls(t, rg, roomB)
}

func TestRecoverReloadsLastToSeeWritesOfAnotherCore(t *testing.T) {
	rg := started(t, baseConfig)
	mustSend(t, rg.Router, cmd(roomA, "alice", "a1"))
	seed(t, rg.msgs, roomA, time.Now(), 2, 3)
	mustRecover(t, rg.Router, roomA, 1)
	expectCalls(t, rg, roomA, "events[1]", "events[2 3]")
	if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "a4")); ack.Seq != 4 {
		t.Fatalf("send after recovery assigned seq %d, want 4", ack.Seq)
	}
}

func TestRecoverSkipsHolesOlderThanTheGraceInPtsOrder(t *testing.T) {
	rg := started(t, baseConfig)
	old := time.Now().Add(-holeGrace - time.Minute)
	seed(t, rg.msgs, roomA, old, 2, 3, 6, 7)
	mustRecover(t, rg.Router, roomA, 0)
	expectCalls(t, rg, roomA, "skip[1]", "events[2 3]", "skip[4 5]", "events[6 7]")
}

func TestRecoverStopsAtAYoungHoleAndSkipsItOnceItAges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		seed(t, rg.msgs, roomA, time.Now().Add(-time.Hour), 1, 2)
		seed(t, rg.msgs, roomA, time.Now(), 4)
		mustRecover(t, rg.Router, roomA, 0)
		expectCalls(t, rg, roomA, "events[1 2]")
		time.Sleep(holeGrace - time.Millisecond)
		mustRecover(t, rg.Router, roomA, 2)
		expectCalls(t, rg, roomA, "events[1 2]")
		time.Sleep(time.Millisecond)
		mustRecover(t, rg.Router, roomA, 2)
		expectCalls(t, rg, roomA, "events[1 2]", "skip[3]", "events[4]")
		rg.cancel()
		_ = rg.wait()
	})
}

func TestRejectedSeqBecomesAHoleThatRecoverySkipsAfterTheGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		rg.sub.then(rg.sub.insert, func(msgs []domain.Message) []store.Result {
			return append([]store.Result{{Outcome: store.Rejected, Err: errors.New("rejected")}}, rg.sub.insert(msgs[1:])...)
		})
		rg.sub.hold()
		first := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "a1"))
		synctest.Wait()
		lost := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "a2"))
		synctest.Wait()
		kept := sendAsync(t.Context(), rg.Router, cmd(roomA, "bob", "a3"))
		synctest.Wait()
		rg.sub.open()
		if r := <-first; r.err != nil || r.ack.Seq != 1 {
			t.Fatalf("first send = %+v, %v; want seq 1", r.ack, r.err)
		}
		if r := <-lost; r.err == nil {
			t.Fatalf("rejected send acked at seq %d", r.ack.Seq)
		}
		if r := <-kept; r.err != nil || r.ack.Seq != 3 {
			t.Fatalf("kept send = %+v, %v; want seq 3", r.ack, r.err)
		}
		mustRecover(t, rg.Router, roomA, 1)
		expectCalls(t, rg, roomA, "events[1]", "events[3]")
		time.Sleep(holeGrace)
		mustRecover(t, rg.Router, roomA, 1)
		expectCalls(t, rg, roomA, "events[1]", "events[3]", "skip[2]", "events[3]")
		rg.cancel()
		_ = rg.wait()
	})
}
