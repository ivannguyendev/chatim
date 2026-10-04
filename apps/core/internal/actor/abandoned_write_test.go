package actor_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type attemptLog struct {
	mu    sync.Mutex
	count int
}

func (l *attemptLog) slowAmbiguous(delay time.Duration) outcome {
	return func(msgs []domain.Message) []store.Result {
		l.mu.Lock()
		l.count++
		l.mu.Unlock()
		time.Sleep(delay)
		return lostUnknown(msgs)
	}
}

func (l *attemptLog) attempts() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count
}

func TestAbandonedWriteNeverLandsAfterAnotherCoreTookTheCID(t *testing.T) {
	w := newWorld(t)
	cfg := clusterConfig
	cfg.GroupDeadline, cfg.ReservationTTL = 300*time.Millisecond, 1500*time.Millisecond
	tries := &attemptLog{}
	sub := &fakeSubmitter{store: w.msgs}
	sub.alwaysDo(tries.slowAmbiguous(120 * time.Millisecond))
	a, _ := runRouter(t, w.msgs, w.rooms, sub, w.registry(t, "core-a", quiet, 0), cfg)

	_, err := a.Send(context.Background(), cmd(roomA, "alice", "x"))
	expectErr(t, err, domain.ErrRetryLater)
	tried := tries.attempts()
	if tried < 1 || tried > 2 {
		t.Fatalf("core A made %d attempts, want the time budget to stop it after at most 2", tried)
	}
	if v := w.cidValue("x"); v != "p:core-a" {
		t.Fatalf("abandoned ambiguous write left %q, want core A's reservation kept", v)
	}

	regB := signalCommits(w.registry(t, "core-b", quiet, 0))
	b := startCore(t, w.msgs, w.rooms, regB)
	_, err = b.Send(context.Background(), cmd(roomA, "alice", "x"))
	expectErr(t, err, domain.ErrRetryLater)
	w.mr.FastForward(dedupe.DefaultPendingTTL)
	ack := mustSend(t, b, cmd(roomA, "alice", "x"))
	awaitSignal(t, "core B cid commit", regB.committed)

	time.Sleep(2 * cfg.GroupDeadline)
	if got := mustSend(t, a, cmd(roomA, "alice", "x")); !sameAck(got, ack) {
		t.Fatalf("core A answered its abandoned cid with %+v, want core B's %+v", got, ack)
	}
	if n := tries.attempts(); n != tried {
		t.Fatalf("core A attempted %d more writes after abandoning", n-tried)
	}
	assertStoredIn(t, w, "x", ack)
}
