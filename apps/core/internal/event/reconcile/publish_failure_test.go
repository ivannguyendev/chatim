package reconcile_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
)

func attemptsOf(js *publishtest.JetStream, id string) int {
	n := 0
	for _, got := range attemptIDs(js) {
		if got == id {
			n++
		}
	}
	return n
}

func TestRefusedPublishEndsTheTermAndTheNextTermReplaysFromTheConfirmedPosition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(tick)
		synctest.Wait()
		refused := false
		rg.js.RefuseWhen(func(m *nats.Msg) error {
			if !refused && publishtest.MsgID(m) == recordID(2) {
				refused = true
				return errors.New("too many stalled")
			}
			return nil
		})
		rg.insert(t, room, 2, 3)
		synctest.Wait()
		if s := rg.Stats(); s.Running || s.Terms != 1 {
			t.Fatalf("stats right after the refusal = %+v, want the first term ended and no new term yet", s)
		}
		if got := rg.sink.Count(failedMsg); got != 1 {
			t.Fatalf("%q logged %d times, want 1", failedMsg, got)
		}
		time.Sleep(5 * tick / 2)
		synctest.Wait()
		if s := rg.Stats(); !s.Running || s.Terms != 2 {
			t.Fatalf("stats after the poll = %+v, want a second running term", s)
		}
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1, 2, 3)) {
			t.Fatalf("stored = %v, want seq 1..3 in order", got)
		}
		if got := attemptsOf(rg.js, recordID(1)); got != 1 {
			t.Fatalf("attempts of the confirmed record = %d, want 1 (no replay before the confirmed position)", got)
		}
		if got := attemptsOf(rg.js, recordID(2)); got != 2 {
			t.Fatalf("attempts of the refused record = %d, want 2 (one per term)", got)
		}
		if got := rg.confirmed(t); got != 3 {
			t.Fatalf("confirmed = %d, want 3", got)
		}
	})
}

func TestFailedAckEndsTheTermWithoutConfirmingAndTheNextTermRepublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1, 2)
		synctest.Wait()
		rg.js.Fail(errors.New("ack timeout"))
		time.Sleep(3 * tick / 2)
		synctest.Wait()
		if s := rg.Stats(); s.Running || s.Terms != 1 {
			t.Fatalf("stats after the next checkpoint = %+v, want the term ended by the failed ack", s)
		}
		if got := rg.confirmed(t); got != 0 {
			t.Fatalf("confirmed after the failed ack = %d, want 0", got)
		}
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1, 2)) {
			t.Fatalf("stored = %v, want seq 1 and 2 republished by the next term", got)
		}
		if got := rg.confirmed(t); got != 2 {
			t.Fatalf("confirmed = %d, want 2", got)
		}
	})
}

func TestPersistentRefusalPublishesOncePerTermAndConfirmsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.RefuseWhen(func(*nats.Msg) error { return errors.New("no responders") })
		rg.insert(t, room, 1)
		time.Sleep(4 * tick)
		synctest.Wait()
		terms := int(rg.Stats().Terms)
		if terms < 3 {
			t.Fatalf("terms = %d, want a new term after each failure", terms)
		}
		if got := len(rg.js.Attempts()); got != terms {
			t.Fatalf("attempts = %d over %d terms, want exactly one per term", got, terms)
		}
		if got := rg.Stats().PublishFailures; got != uint64(terms) {
			t.Fatalf("publish failures = %d over %d terms, want one per term", got, terms)
		}
		if got := rg.sink.Count(failedMsg); got < 1 || got > terms {
			t.Fatalf("%q logged %d times over %d terms, want at least once and at most once per second", failedMsg, got, terms)
		}
		if got := rg.confirmed(t); got != 0 || len(rg.js.Stored()) != 0 {
			t.Fatalf("confirmed = %d, stored = %v; want nothing past a refused publish", got, storedIDs(rg.js))
		}
	})
}
