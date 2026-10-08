package actor_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestFullMailboxIsBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := baseConfig
		cfg.Mailbox = 2
		rg := newRig(t, cfg)
		rg.sub.hold()
		rg.start(t)
		ctx := t.Context()
		waits := []<-chan sendResult{sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1"))}
		synctest.Wait()
		waits = append(waits,
			sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c2")),
			sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c3")))
		synctest.Wait()
		_, err := rg.Send(ctx, cmd(roomA, "alice", "c4"))
		expectErr(t, err, domain.ErrBusy)
		mustSendAfterOpen(t, rg, waits)
	})
}

func TestActorLimitIsBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := baseConfig
		cfg.MaxActors, cfg.Idle = 1, time.Second
		rg := started(t, cfg)
		mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		_, err := rg.Send(t.Context(), cmd(roomB, "alice", "c2"))
		expectErr(t, err, domain.ErrBusy)
		mustSend(t, rg.Router, cmd(roomA, "alice", "c3"))
		time.Sleep(cfg.Idle)
		synctest.Wait()
		mustSend(t, rg.Router, cmd(roomB, "alice", "c2"))
	})
}

func TestCancelledCallerDoesNotAbortItsSharedGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		bg := t.Context()
		busy := sendAsync(bg, rg.Router, cmd(roomA, "bob", "busy"))
		synctest.Wait()
		ctx, cancel := context.WithCancel(bg)
		gone := sendAsync(ctx, rg.Router, cmd(roomA, "alice", "c1"))
		synctest.Wait()
		kept := sendAsync(bg, rg.Router, cmd(roomA, "bob", "c2"))
		synctest.Wait()
		rg.sub.release()
		synctest.Wait()

		cancel()
		expectErr(t, (<-gone).err, context.Canceled)
		rg.sub.release()
		if got := <-kept; got.err != nil || got.ack.Seq != 3 {
			t.Fatalf("other caller in the group got %+v, %v; want seq 3", got.ack, got.err)
		}
		if got := <-busy; got.err != nil {
			t.Fatalf("busy: %v", got.err)
		}
		assertGroupSeqs(t, rg, []uint64{1}, []uint64{2, 3})
		rg.sub.open()
		retry := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if retry.Seq != 2 {
			t.Fatalf("retry of the abandoned cid got seq %d, want the stored 2", retry.Seq)
		}
		assertStoredOnce(t, rg, "c1", retry)
	})
}

func mustSendAfterOpen(t *testing.T, rg *rig, waits []<-chan sendResult) {
	t.Helper()
	rg.sub.open()
	for i, w := range waits {
		if got := <-w; got.err != nil {
			t.Fatalf("queued command %d: %v", i, got.err)
		}
	}
}
