package reconcile_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestRepublishesAnUnmarkedChangeOnlyAfterTheDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(delay - time.Millisecond)
		synctest.Wait()
		if got := attemptIDs(rg.js); len(got) != 0 {
			t.Fatalf("attempts before the delay = %v, want none", got)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		stored := rg.js.Stored()
		if len(stored) != 1 || stored[0].Subject != "evt.acme.room.4242.msg_created" || storedIDs(rg.js)[0] != eventID(1) {
			t.Fatalf("stored = %v, want one %s on evt.acme.room.4242.msg_created", storedIDs(rg.js), eventID(1))
		}
	})
}

func TestSkipsMarkedChangesAndConfirmsPastThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.marks.mark(store.MsgKey{Room: room, Seq: 1}, store.MsgKey{Room: room, Seq: 3})
		rg.insert(t, room, 1, 2, 3)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); !slices.Equal(got, []string{eventID(2)}) {
			t.Fatalf("attempts = %v, want only %s", got, eventID(2))
		}
		if got := rg.confirmed(t); got != 3 {
			t.Fatalf("confirmed = %d, want 3", got)
		}
	})
}

func TestConfirmWaitsForTheAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1)
		time.Sleep(delay + 2*tick)
		synctest.Wait()
		if got := rg.confirmed(t); got != 0 {
			t.Fatalf("confirmed before the ack = %d, want 0", got)
		}
		rg.js.Release()
		time.Sleep(tick)
		synctest.Wait()
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed after the ack = %d, want 1", got)
		}
	})
}

func TestRefusedPublishIsResent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		refused := 0
		rg.js.RefuseWhen(func(*nats.Msg) error {
			if refused == 0 {
				refused++
				return errors.New("too many stalled")
			}
			return nil
		})
		rg.insert(t, room, 1)
		time.Sleep(delay + 2*tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); len(got) < 2 {
			t.Fatalf("attempts = %v, want the refused publish resent", got)
		}
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("stored = %v, want %s once", got, eventID(1))
		}
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed = %d, want 1", got)
		}
	})
}

func TestMarkLookupFailurePublishesEverything(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.marks.mark(store.MsgKey{Room: room, Seq: 1})
		rg.marks.fail(errors.New("redis down"))
		rg.insert(t, room, 1)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("stored = %v, want %s", got, eventID(1))
		}
	})
}

func TestUnknownRoomIsDroppedAndPassed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, 999, 1)
		rg.insert(t, room, 1)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("stored = %v, want only %s", got, eventID(1))
		}
		if rg.Dropped() != 1 || rg.sink.Count(dropMsg) != 1 {
			t.Fatalf("dropped = %d, logged %d; want 1 and 1", rg.Dropped(), rg.sink.Count(dropMsg))
		}
		if got := rg.confirmed(t); got != 2 {
			t.Fatalf("confirmed = %d, want 2", got)
		}
	})
}

func TestFullWindowWaitsForTheHead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1, 2, 3, 4, 5, 6)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := len(rg.js.Attempts()); got != setup.Window {
			t.Fatalf("attempts with a full window = %d, want %d", got, setup.Window)
		}
		rg.js.Release()
		time.Sleep(tick)
		synctest.Wait()
		if got := len(rg.js.Stored()); got != 6 {
			t.Fatalf("stored after release = %d, want 6", got)
		}
	})
}
