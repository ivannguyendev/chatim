package reconcile_test

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestStatsTrackTermsAndForwards(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1, 2)
		rg.createRoom(t, otherRoom)
		time.Sleep(tick)
		synctest.Wait()
		if s := rg.Stats(); !s.Running || s.Terms != 1 || s.Forwarded != 3 || s.Dropped != 0 {
			t.Fatalf("stats = %+v, want running, 1 term, 3 forwarded, 0 dropped", s)
		}
		rg.owner.leading.Store(false)
		time.Sleep(2 * tick)
		synctest.Wait()
		if s := rg.Stats(); s.Running {
			t.Fatalf("stats after losing the lead = %+v, want not running", s)
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
