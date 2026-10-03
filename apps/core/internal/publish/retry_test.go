package publish_test

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
)

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
	eventually(t, "three events stored", func() bool { return len(rg.js.Stored()) == 3 })
	if n := attemptsOf(rg.js, "101-0-2"); n != 3 {
		t.Fatalf("seq 2 attempted %d times, want 3", n)
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
	eventually(t, "both events stored", func() bool { return len(rg.js.Stored()) == 2 })
	if a, b := attemptsOf(rg.js, "101-0-1"), attemptsOf(rg.js, "101-0-2"); a != 2 || b != 2 {
		t.Fatalf("attempts: refused seq %d, unacked seq %d, want 2 each", a, b)
	}
}
