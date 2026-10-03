package publish_test

import (
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
)

func TestWatermarkNeverSkipsAPtsWhosePublishFailed(t *testing.T) {
	rg := started(t, fastSetup)
	rg.js.NackWhen(nackIDs("101-0-3"))
	rg.enqueue(t, roomA, span(1, 5)...)

	waitWatermark(t, rg.mr, roomA, 2)
	eventually(t, "pts 3 abandoned", func() bool { return rg.sink.Count(abandonedMsg) == 1 })
	holdsWatermark(t, rg.mr, roomA, 2)
	if got, want := storedIDs(rg.js), []string{"101-0-1", "101-0-2", "101-0-4", "101-0-5"}; !slices.Equal(got, want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
	if n := attemptsOf(rg.js, "101-0-3"); n != fastSetup.Attempts {
		t.Fatalf("pts 3 attempted %d times, want %d", n, fastSetup.Attempts)
	}

	rg.js.NackWhen(nil)
	rg.enqueue(t, roomA, 3)
	waitWatermark(t, rg.mr, roomA, 5)
}

func TestFailedPublishIsRetriedUntilAcked(t *testing.T) {
	rg := started(t, fastSetup)
	nacks := 2
	rg.js.NackWhen(func(m *nats.Msg) error {
		if publishtest.MsgID(m) == "101-0-2" && nacks > 0 {
			nacks--
			return errNack
		}
		return nil
	})
	rg.enqueue(t, roomA, 1, 2, 3)
	waitWatermark(t, rg.mr, roomA, 3)
	if n := attemptsOf(rg.js, "101-0-2"); n != 3 {
		t.Fatalf("pts 2 attempted %d times, want 3", n)
	}
	if n := rg.sink.Count(abandonedMsg); n != 0 {
		t.Fatalf("logged %d abandoned publishes for a publish that succeeded on retry", n)
	}
}

func TestRefusedAndUnackedPublishesAreRetried(t *testing.T) {
	cfg := fastSetup
	cfg.AckTimeout = 20 * time.Millisecond
	rg := started(t, cfg)
	refused, silenced := false, false
	rg.js.RefuseWhen(func(m *nats.Msg) error {
		if publishtest.MsgID(m) == "101-0-1" && !refused {
			refused = true
			return nats.ErrConnectionClosed
		}
		return nil
	})
	rg.js.SilenceWhen(func(m *nats.Msg) bool {
		if publishtest.MsgID(m) == "101-0-2" && !silenced {
			silenced = true
			return true
		}
		return false
	})
	rg.enqueue(t, roomA, 1, 2)
	waitWatermark(t, rg.mr, roomA, 2)
	if a, b := attemptsOf(rg.js, "101-0-1"), attemptsOf(rg.js, "101-0-2"); a != 2 || b != 2 {
		t.Fatalf("attempts: refused pts %d, unacked pts %d, want 2 each", a, b)
	}
}

func TestTwoPublishersKeepRedisAtTheLargestContiguousPrefix(t *testing.T) {
	mr := miniredis.RunT(t)
	a := newRig(t, fastSetup, mr).start(t)
	b := newRig(t, fastSetup, mr).start(t)

	a.enqueue(t, roomA, 1, 2, 3)
	waitWatermark(t, mr, roomA, 3)
	b.enqueue(t, roomA, 4, 5)
	waitWatermark(t, mr, roomA, 5)

	a.js.NackWhen(nackIDs("101-0-6"))
	a.enqueue(t, roomA, 6)
	eventually(t, "pts 6 abandoned", func() bool { return a.sink.Count(abandonedMsg) == 1 })
	b.enqueue(t, roomA, 7)
	eventually(t, "pts 7 stored", func() bool { return slices.Contains(storedIDs(b.js), "101-0-7") })
	holdsWatermark(t, mr, roomA, 5)

	a.js.NackWhen(nil)
	a.enqueue(t, roomA, 6)
	eventually(t, "pts 6 stored", func() bool { return slices.Contains(storedIDs(a.js), "101-0-6") })
	holdsWatermark(t, mr, roomA, 5)

	b.enqueue(t, roomA, 6, 7)
	waitWatermark(t, mr, roomA, 7)
}
