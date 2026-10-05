package actor_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestSeqContentionBacksOffBetweenReassignCycles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.alwaysDo(rg.sub.foreignFirst)
		rg.start(t)
		begin := time.Now()
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if waited := time.Since(begin); waited < 17500*time.Microsecond {
			t.Fatalf("three reassign cycles took %v, want at least 17.5ms of jittered backoff", waited)
		}
	})
}

func TestExhaustedContentionYieldsTheRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.alwaysDo(rg.sub.foreignFirst)
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		synctest.Wait()
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("actors after exhausted contention = %d, want 0", n)
		}
		if got := rg.Stats().Yields; got != 1 {
			t.Fatalf("yields = %d, want 1", got)
		}
	})
}

func TestCIDPendingOnAnotherCoreIsCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.cids.force(dedupe.Key{Room: roomA, User: "alice", CID: "c1"}, dedupe.PendingElsewhere, dedupe.Record{})
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if got := rg.Stats().CIDElsewhere; got != 1 {
			t.Fatalf("cid pending elsewhere = %d, want 1", got)
		}
	})
}
