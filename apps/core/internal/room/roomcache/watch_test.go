package roomcache

import (
	"log/slog"
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

func expectForgotten(t *testing.T, w *Watch, f *forgotten, want ...uint64) {
	t.Helper()
	if got := f.got(); !slices.Equal(got, want) {
		t.Errorf("forgotten rooms = %v, want %v", got, want)
	}
	if got := w.Forgets(); got != uint64(len(want)) {
		t.Errorf("Forgets() = %d, want %d", got, len(want))
	}
}

func TestRoomUpdatedAndMemberCountChangedForgetTheRoomInfo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		info := newInfo(t, 10)
		info.Put(info.Mark(), room(101, "a"), room(202, "b"), room(303, "c"))
		bus := &fakeBus{}
		w := startWatch(t, bus, info.Forget)
		defer w.Stop()
		bus.publish("live.acme.room.101.evt.room_updated")
		bus.publish("live.acme.room.202.evt.member_count_changed")
		synctest.Wait()
		if _, misses := info.Get([]uint64{101, 202, 303}); !slices.Equal(misses, []uint64{101, 202}) {
			t.Errorf("misses = %v, want rooms 101 and 202 forgotten", misses)
		}
		if w.Forgets() != 2 {
			t.Errorf("Forgets() = %d, want 2", w.Forgets())
		}
	})
}

func TestOtherRoomEventsAreIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, f := &fakeBus{}, &forgotten{}
		w := startWatch(t, bus, f.forget)
		defer w.Stop()
		for _, subject := range []string{
			"live.acme.room.101.evt.room_created",
			"live.acme.room.101.evt.msg_pinned",
			"live.acme.member.101.evt.room_updated",
			"live.acme.message.101.evt.member_count_changed",
			"evt.acme.room.101.room_updated",
			"other.acme.room.101.evt.room_updated",
		} {
			bus.publish(subject)
		}
		synctest.Wait()
		expectForgotten(t, w, f)
		if got := w.Malformed(); got != 0 {
			t.Errorf("Malformed() = %d, want 0", got)
		}
	})
}

func TestABrokenRoomTokenIsCountedAndSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, f := &fakeBus{}, &forgotten{}
		w := startWatch(t, bus, f.forget)
		defer w.Stop()
		for _, id := range []string{"abc", "-1", "0", "18446744073709551616", "1x"} {
			bus.publish("live.acme.room." + id + ".evt.room_updated")
		}
		bus.publish("live.acme.room.7.evt.member_count_changed")
		synctest.Wait()
		expectForgotten(t, w, f, 7)
		if got := w.Malformed(); got != 5 {
			t.Errorf("Malformed() = %d, want 5", got)
		}
	})
}

func TestStopUnsubscribesTheRoomWatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, f := &fakeBus{}, &forgotten{}
		w := startWatch(t, bus, f.forget)
		want := []string{"live.*.room.*.evt.room_updated", "live.*.room.*.evt.member_count_changed"}
		if !slices.Equal(bus.patterns, want) {
			t.Fatalf("subscribed %v, want %v", bus.patterns, want)
		}
		if err := w.Start(t.Context()); err == nil {
			t.Error("second Start succeeded, want an error")
		}
		w.Stop()
		if n := bus.live(); n != 0 {
			t.Fatalf("%d subscriptions left after Stop, want 0", n)
		}
		bus.publish("live.acme.room.101.evt.room_updated")
		synctest.Wait()
		expectForgotten(t, w, f)
		w.Stop()
	})
}

func TestAFailedRoomWatchSubscribeLeavesNothingSubscribed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := &fakeBus{failOn: "live.*.room.*.evt.member_count_changed"}
		w, err := newWatch(bus, "live", (&forgotten{}).forget, quiet)
		if err != nil {
			t.Fatalf("newWatch: %v", err)
		}
		if err := w.Start(t.Context()); err == nil {
			t.Fatal("Start succeeded, want the subscribe error")
		}
		if n := bus.live(); n != 0 {
			t.Fatalf("%d subscriptions left after a failed Start, want 0", n)
		}
		w.Stop()
	})
}

func TestNewWatchRejectsBadArguments(t *testing.T) {
	forget := func(uint64) {}
	cases := map[string]struct {
		root   string
		forget func(uint64)
		log    *slog.Logger
	}{
		"empty root":     {"", forget, quiet},
		"dotted root":    {"live.x", forget, quiet},
		"wildcard root":  {"*", forget, quiet},
		"missing forget": {"live", nil, quiet},
		"missing log":    {"live", forget, nil},
	}
	for name, c := range cases {
		if _, err := newWatch(&fakeBus{}, c.root, c.forget, c.log); err == nil {
			t.Errorf("%s: newWatch succeeded, want an error", name)
		}
	}
	if _, err := NewWatch(nil, "live", forget, quiet); err == nil {
		t.Error("NewWatch with a nil connection succeeded, want an error")
	}
}
