package actor_test

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestDuplicateQueuedWithPendingCIDSharesItsOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		ctx := t.Context()
		busy := sendAsync(ctx, rg.Router, cmd(roomA, "bob", "busy"))
		synctest.Wait()
		first := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1"))
		synctest.Wait()
		second := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1"))
		other := sendAsync(ctx, rg.Router, cmd(roomA, "bob", "c1"))
		synctest.Wait()
		rg.sub.release()
		synctest.Wait()
		rg.sub.release()
		if (<-busy).err != nil {
			t.Fatal("busy message failed")
		}
		a, b, c := <-first, <-second, <-other
		if a.err != nil || b.err != nil || c.err != nil {
			t.Fatalf("errors: %v, %v, %v", a.err, b.err, c.err)
		}
		if !sameAck(a.ack, b.ack) {
			t.Fatalf("same cid got acks %+v and %+v", a.ack, b.ack)
		}
		if c.ack.Seq == a.ack.Seq {
			t.Fatalf("another user's cid c1 shared seq %d", c.ack.Seq)
		}
		groups := rg.sub.sent()
		if len(groups) != 2 || len(groups[1]) != 2 {
			t.Fatalf("groups = %v, want [busy] then [alice/c1, bob/c1]", groups)
		}
		rg.sub.open()
		if again := mustSend(t, rg.Router, cmd(roomA, "alice", "c1")); !sameAck(again, a.ack) {
			t.Fatalf("resend after commit got %+v, want %+v", again, a.ack)
		}
		if n := len(rg.sub.sent()); n != 2 {
			t.Fatalf("resend after commit submitted a new group (%d groups)", n)
		}
		docs := storedCIDs(t, rg.msgs.Messages, roomA)
		if len(docs["c1"]) != 2 {
			t.Fatalf("stored %d messages with cid c1, want one per user", len(docs["c1"]))
		}
	})
}

func TestFailedCIDIsForgottenSoItCanBeRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.then(rejected)
		rg.start(t)
		if _, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1")); err == nil {
			t.Fatal("rejected write was acked")
		}
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 1 {
			t.Fatalf("retry after a rejected write got seq %d, want 1", ack.Seq)
		}
		if n := len(rg.sub.sent()); n != 2 {
			t.Fatalf("submitted %d groups, want 2", n)
		}
	})
}

func TestSameCIDFromConcurrentCallersIsStoredOnce(t *testing.T) {
	cl := newCluster(t, 1)
	const callers = 8
	waits := make([]<-chan sendResult, callers)
	for i := range waits {
		waits[i] = sendAsync(context.Background(), cl.cores[0], cmd(roomA, "u0", "dup"))
	}
	var first sendResult
	for i, w := range waits {
		got := <-w
		if got.err != nil {
			t.Fatalf("caller %d: %v", i, got.err)
		}
		if i == 0 {
			first = got
		}
		if !sameAck(got.ack, first.ack) {
			t.Fatalf("caller %d got %+v, want %+v", i, got.ack, first.ack)
		}
	}
	if again := mustSend(t, cl.cores[0], cmd(roomA, "u0", "dup")); !sameAck(again, first.ack) {
		t.Fatalf("resend after commit got %+v, want %+v", again, first.ack)
	}
	docs := storedCIDs(t, cl.msgs, roomA)["dup"]
	if len(docs) != 1 {
		t.Fatalf("stored %d copies, want 1", len(docs))
	}
	assertAckMatches(t, first.ack, docs[0])
}
