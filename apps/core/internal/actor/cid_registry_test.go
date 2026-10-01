package actor_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestRemoteCommittedCIDIsAnsweredWithoutInsert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rec := dedupe.Record{Seq: 7, Pts: 7, CreatedAt: time.UnixMilli(1_700_000_000_123).UTC()}
		rg.cids.force(remoteKey(roomA, "alice", "x"), dedupe.Committed, rec)
		rg.start(t)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "x"))
		if !sameAck(ack, actor.Ack(rec)) {
			t.Fatalf("ack %+v, want the committed record %+v", ack, rec)
		}
		if again := mustSend(t, rg.Router, cmd(roomA, "alice", "x")); !sameAck(again, ack) {
			t.Fatalf("resend got %+v, want %+v", again, ack)
		}
		if n := len(rg.sub.sent()); n != 0 {
			t.Fatalf("a remotely committed cid submitted %d groups", n)
		}
		if reserves, _, _ := rg.cids.calls(); len(reserves) != 1 {
			t.Fatalf("Reserve called %d times, want once before the local cache took over", len(reserves))
		}
	})
}

func TestRemotelyPendingCIDFailsEveryWaiterWithRetryLater(t *testing.T) {
	for _, status := range []dedupe.Status{dedupe.PendingElsewhere, dedupe.PendingHere} {
		t.Run(status.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rg := newRig(t, baseConfig)
				rg.cids.force(remoteKey(roomA, "alice", "x"), status, dedupe.Record{})
				rg.sub.hold()
				rg.start(t)
				ctx := t.Context()
				busy := sendAsync(ctx, rg.Router, cmd(roomA, "bob", "busy"))
				synctest.Wait()
				first := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "x"))
				synctest.Wait()
				second := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "x"))
				other := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "y"))
				synctest.Wait()
				rg.sub.release()
				synctest.Wait()
				rg.sub.release()
				expectErr(t, (<-first).err, domain.ErrRetryLater)
				expectErr(t, (<-second).err, domain.ErrRetryLater)
				if got := <-other; got.err != nil || got.ack.Seq != 2 {
					t.Fatalf("other cid in the group got %+v, %v; want seq 2", got.ack, got.err)
				}
				if got := <-busy; got.err != nil {
					t.Fatalf("busy: %v", got.err)
				}
				reserves, _, aborts := rg.cids.calls()
				want := [][]dedupe.Key{{remoteKey(roomA, "bob", "busy")}, {remoteKey(roomA, "alice", "x"), remoteKey(roomA, "alice", "y")}}
				if !slices.EqualFunc(reserves, want, slices.Equal) || len(aborts) != 0 {
					t.Fatalf("reserves %v aborts %v, want %v and none", reserves, aborts, want)
				}
				assertGroupSeqs(t, rg, []uint64{1}, []uint64{2})
			})
		})
	}
}

func TestGroupReservesOnceAndRecordsCommitsBeforeAcking(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		ctx := t.Context()
		busy := sendAsync(ctx, rg.Router, cmd(roomA, "bob", "busy"))
		synctest.Wait()
		cids := []string{"a1", "a2", "a3"}
		var waits []<-chan sendResult
		for _, cid := range cids {
			waits = append(waits, sendAsync(ctx, rg.Router, cmd(roomA, "alice", cid)))
			synctest.Wait()
		}
		rg.sub.release()
		<-busy
		synctest.Wait()
		rg.sub.release()
		for i, w := range waits {
			got := <-w
			if got.err != nil {
				t.Fatalf("%s: %v", cids[i], got.err)
			}
			_, commits, _ := rg.cids.calls()
			if len(commits) != 2 || len(commits[1]) != len(cids) {
				t.Fatalf("commits when %s was acked: %v, want [busy] then one batch of %d", cids[i], commits, len(cids))
			}
			rec := commits[1][i]
			if rec.Key != remoteKey(roomA, "alice", cids[i]) || !sameAck(actor.Ack(rec.Record), got.ack) {
				t.Fatalf("recorded %+v for ack %+v of %s", rec, got.ack, cids[i])
			}
		}
		if reserves, _, _ := rg.cids.calls(); len(reserves) != 2 || len(reserves[1]) != len(cids) {
			t.Fatalf("reserves = %v, want one call per group", reserves)
		}
	})
}

func TestRegistryFailureFallsBackToTheLocalCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.cids.fail(errors.New("redis down"))
		rg.sub.then(rg.sub.insert, rejected)
		rg.start(t)
		first := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if again := mustSend(t, rg.Router, cmd(roomA, "alice", "c1")); !sameAck(again, first) {
			t.Fatalf("resend during the outage got %+v, want %+v", again, first)
		}
		if _, err := rg.Send(t.Context(), cmd(roomA, "alice", "c2")); err == nil {
			t.Fatal("a rejected write was acked")
		}
		assertStoredOnce(t, rg, "c1", first)
		if _, _, aborts := rg.cids.calls(); len(aborts) != 0 {
			t.Fatalf("released cids that were never reserved: %v", aborts)
		}
	})
}

func TestLocalAckExpiresAfterTenMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := baseConfig
		cfg.Idle = time.Hour
		rg := started(t, cfg)
		first := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		time.Sleep(10*time.Minute - time.Millisecond)
		mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if reserves, _, _ := rg.cids.calls(); len(reserves) != 1 {
			t.Fatalf("resend within ten minutes reached the registry (%d reserves)", len(reserves))
		}
		time.Sleep(time.Millisecond)
		again := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if reserves, _, _ := rg.cids.calls(); len(reserves) != 2 {
			t.Fatalf("resend after ten minutes did not consult the registry (%d reserves)", len(reserves))
		}
		if !sameAck(again, first) || len(rg.sub.sent()) != 1 {
			t.Fatalf("resend after expiry got %+v after %d groups, want %+v from the registry", again, len(rg.sub.sent()), first)
		}
	})
}
