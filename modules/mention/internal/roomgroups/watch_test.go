package roomgroups

import (
	"slices"
	"testing"
	"testing/synctest"
)

func startWatch(t *testing.T, bus *fakeBus, forget func(uint64)) *Watch {
	t.Helper()
	w, err := newWatch(bus, "live", forget, quiet)
	if err != nil {
		t.Fatalf("newWatch: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return w
}

func TestMemberAddedAndRemovedForgetASmallRoomSet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := newSets(t)
		of(t, s, smallRoom, 1)
		of(t, s, bigRoom, 9)
		synctest.Wait()
		bus := &fakeBus{}
		w := startWatch(t, bus, s.Forget)
		defer w.Stop()
		f.set(smallRoom, "hr")
		bus.publish("live.acme.member.101.evt.member_added")
		synctest.Wait()
		expectSet(t, "after member_added", of(t, s, smallRoom, 1), true, "hr")
		f.set(smallRoom, "dev")
		f.set(bigRoom, "dev")
		bus.publish("live.acme.member.101.evt.member_removed")
		bus.publish("live.acme.member.202.evt.member_removed")
		synctest.Wait()
		expectSet(t, "after member_removed", of(t, s, smallRoom, 1), true, "dev")
		expectSet(t, "big room", of(t, s, bigRoom, 9), true, "dev", "ops")
		if w.Forgets() != 3 || w.Malformed() != 0 {
			t.Fatalf("forgets/malformed = %d/%d, want 3/0", w.Forgets(), w.Malformed())
		}
	})
}

func TestOtherMemberEventsAndBrokenSubjectsAreSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, f := &fakeBus{}, &forgotten{}
		w := startWatch(t, bus, f.forget)
		for _, subject := range []string{
			"live.acme.member.101.evt.member_role_changed",
			"live.acme.room.101.evt.member_added",
			"evt.acme.member.101.member_added",
			"other.acme.member.101.evt.member_added",
		} {
			bus.publish(subject)
		}
		bus.publish("live.acme.member.abc.evt.member_added")
		bus.publish("live.acme.member.0.evt.member_removed")
		synctest.Wait()
		if got := f.got(); len(got) != 0 || w.Malformed() != 2 {
			t.Fatalf("forgotten %v malformed %d, want none and 2", got, w.Malformed())
		}
		if !slices.Equal(bus.patterns, []string{"live.*.member.*.evt.member_added", "live.*.member.*.evt.member_removed"}) {
			t.Fatalf("patterns = %v", bus.patterns)
		}
		w.Stop()
		if bus.live() != 0 {
			t.Fatalf("live subscriptions after Stop = %d", bus.live())
		}
	})
}

func TestWatchStartFailsCleanly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := &fakeBus{failOn: "live.*.member.*.evt.member_removed"}
		w, err := newWatch(bus, "live", (&forgotten{}).forget, quiet)
		if err != nil {
			t.Fatalf("newWatch: %v", err)
		}
		if err := w.Start(t.Context()); err == nil {
			t.Fatal("Start succeeded although a subscribe failed")
		}
		if bus.live() != 0 {
			t.Fatalf("live subscriptions after a failed Start = %d", bus.live())
		}
		if _, err := newWatch(bus, "a.b", (&forgotten{}).forget, quiet); err == nil {
			t.Fatal("newWatch accepted a dotted live root")
		}
		if _, err := NewWatch(nil, "live", (&forgotten{}).forget, quiet); err == nil {
			t.Fatal("NewWatch accepted a nil connection")
		}
		ok := startWatch(t, &fakeBus{}, (&forgotten{}).forget)
		defer ok.Stop()
		if err := ok.Start(t.Context()); err == nil {
			t.Fatal("second Start succeeded")
		}
	})
}
