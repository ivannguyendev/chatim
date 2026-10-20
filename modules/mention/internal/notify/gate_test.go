package notify_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/notify"
)

func TestGateAgeEdge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stats := &notify.Stats{}
		g := notify.NewGate(5*time.Minute, stats)
		now := time.Now()
		truth(t, g.Fresh(now))
		truth(t, g.Fresh(now.Add(-5*time.Minute)))
		truth(t, !(g.Fresh(now.Add(-5*time.Minute - time.Nanosecond))))
		truth(t, g.Fresh(now.Add(time.Minute)))
		equal(t, uint64(1), stats.Skipped(notify.ReasonStale))
		equal(t, stats.Skipped(notify.ReasonSuperseded), uint64(0))
	})
}

func TestGateSuperseded(t *testing.T) {
	stats := &notify.Stats{}
	g := notify.NewGate(5*time.Minute, stats)
	truth(t, g.Latest(2, 2))
	truth(t, g.Latest(3, 2))
	truth(t, g.Latest(0, 0))
	truth(t, !(g.Latest(1, 2)))
	equal(t, uint64(1), stats.Skipped(notify.ReasonSuperseded))
	equal(t, stats.Skipped(notify.ReasonStale), uint64(0))
	equal(t, stats.Skipped("other"), uint64(0))
}
