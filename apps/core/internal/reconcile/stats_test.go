package reconcile_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestStatsTrackTermsRepublishesAndLag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.marks.mark(store.MsgKey{Room: room, Seq: 1})
		rg.insert(t, room, 1, 2)
		time.Sleep(delay + tick)
		synctest.Wait()
		s := rg.Stats()
		if !s.Running || s.Terms != 1 || s.Republished != 1 || s.Lag < delay {
			t.Fatalf("stats = %+v, want running, 1 term, 1 republished, lag >= %v", s, delay)
		}
		rg.owner.leading.Store(false)
		time.Sleep(2 * tick)
		synctest.Wait()
		if s := rg.Stats(); s.Running || s.Lag != 0 {
			t.Fatalf("stats after losing the lead = %+v, want not running and zero lag", s)
		}
	})
}

func TestStatsCountLostHistory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		rg.feed.LoseHistory()
		rg.start(t)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.Stats().HistoryLost; got != 1 {
			t.Fatalf("history lost = %d, want 1", got)
		}
	})
}
