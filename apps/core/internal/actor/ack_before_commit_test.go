package actor_test

import (
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
)

func TestAckDoesNotWaitForTheCidCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		release := rg.cids.holdCommits()
		t.Cleanup(release)
		rg.start(t)
		w := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "x"))
		synctest.Wait()
		var got sendResult
		var acked bool
		select {
		case got = <-w:
			acked = true
		default:
		}
		release()
		synctest.Wait()
		if !acked {
			<-w
			t.Fatal("Send waited for the cid commit before answering the ack")
		}
		if got.err != nil {
			t.Fatalf("Send: %v", got.err)
		}
		_, commits, _ := rg.cids.calls()
		want := dedupe.Entry{Key: remoteKey(roomA, "alice", "x"), Record: dedupe.Record(got.ack)}
		if len(commits) != 1 || len(commits[0]) != 1 || commits[0][0].Key != want.Key || !sameAck(actor.Ack(commits[0][0].Record), got.ack) {
			t.Fatalf("commits %v, want one batch of %+v", commits, want)
		}
	})
}

func TestEventsAreEnqueuedBeforeTheCidCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		release := rg.cids.holdCommits()
		t.Cleanup(release)
		rg.start(t)
		w := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "x"))
		synctest.Wait()
		evs := rg.events.events(roomA)
		_, commits, _ := rg.cids.calls()
		release()
		synctest.Wait()
		ack := <-w
		if ack.err != nil {
			t.Fatalf("Send: %v", ack.err)
		}
		if len(evs) != 1 || evs[0].GetSeq() != ack.ack.Seq || len(commits) != 0 {
			t.Fatalf("while the commit was held: %d events, %d commits; want the event enqueued and no commit yet", len(evs), len(commits))
		}
		if _, after, _ := rg.cids.calls(); len(after) != 1 {
			t.Fatalf("commits after release = %d, want 1", len(after))
		}
	})
}
