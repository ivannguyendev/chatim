package effects_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestAcksKnownKindsThatHaveNoEffects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		at := time.Now().Add(-delay)
		rg.broker.Publish(0, work.Record{Kind: store.BookmarkChanged, Room: 1, Seq: 1, Version: 1, User: "alice", CommittedAt: at})
		rg.broker.Publish(0, work.Record{Kind: store.MessageCountCheck, Room: 1, Seq: 1, Version: 7, User: "replies", CommittedAt: at})
		rg.start(t)
		synctest.Wait()
		if a, n := rg.broker.Acked(), rg.broker.Naked(); len(a) != 2 || len(n) != 0 {
			t.Fatalf("acked %v, naked %v; want both records acked with nothing to run", a, n)
		}
		if s := rg.Stats(); s.Processed != 2 || s.Failed != 0 {
			t.Fatalf("stats = %+v, want 2 processed", s)
		}
	})
}
