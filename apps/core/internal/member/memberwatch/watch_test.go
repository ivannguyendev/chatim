package memberwatch

import (
	"log/slog"
	"slices"
	"testing"
	"testing/synctest"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

var quiet = slog.New(slog.DiscardHandler)

func startWatch(t *testing.T, bus *fakeBus, f *forgotten) *Watch {
	t.Helper()
	w, err := newWatch(bus, "live", f.forget, quiet)
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

func TestAMemberRemovedSubjectForgetsItsRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, f := &fakeBus{}, &forgotten{}
		w := startWatch(t, bus, f)
		defer w.Stop()
		bus.publish("live.acme.member.101.evt.member_removed")
		synctest.Wait()
		expectForgotten(t, w, f, 101)
	})
}

func TestARoleChangedSubjectForgetsItsRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, f := &fakeBus{}, &forgotten{}
		w := startWatch(t, bus, f)
		defer w.Stop()
		bus.publish("live.acme.member.18446744073709551614.evt.member_role_changed")
		synctest.Wait()
		expectForgotten(t, w, f, 18446744073709551614)
	})
}

func TestOtherMemberEventsAreIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, f := &fakeBus{}, &forgotten{}
		w := startWatch(t, bus, f)
		defer w.Stop()
		for _, subject := range []string{
			"live.acme.member.101.evt.member_added",
			"live.acme.member.101.evt.member_left",
			"live.acme.member.101.evt.member_priority_changed",
			"live.acme.message.101.evt.member_removed",
			"evt.acme.member.101.member_removed",
			"other.acme.member.101.evt.member_removed",
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
		w := startWatch(t, bus, f)
		defer w.Stop()
		for _, room := range []string{"abc", "-1", "0", "18446744073709551616", "1x"} {
			bus.publish("live.acme.member." + room + ".evt.member_removed")
		}
		bus.publish("live.acme.member.7.evt.member_role_changed")
		synctest.Wait()
		expectForgotten(t, w, f, 7)
		if got := w.Malformed(); got != 5 {
			t.Errorf("Malformed() = %d, want 5", got)
		}
	})
}

func TestStopUnsubscribes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus, f := &fakeBus{}, &forgotten{}
		w := startWatch(t, bus, f)
		want := []string{"live.*.member.*.evt.member_removed", "live.*.member.*.evt.member_role_changed"}
		if !slices.Equal(bus.patterns, want) {
			t.Fatalf("subscribed %v, want %v", bus.patterns, want)
		}
		w.Stop()
		if n := bus.live(); n != 0 {
			t.Fatalf("%d subscriptions left after Stop, want 0", n)
		}
		bus.publish("live.acme.member.101.evt.member_removed")
		synctest.Wait()
		expectForgotten(t, w, f)
		w.Stop()
	})
}

func TestAFailedSubscribeLeavesNothingSubscribed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := &fakeBus{failOn: "live.*.member.*.evt.member_role_changed"}
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

func TestStartTwiceIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := &fakeBus{}
		w := startWatch(t, bus, &forgotten{})
		defer w.Stop()
		if err := w.Start(t.Context()); err == nil {
			t.Fatal("second Start succeeded, want an error")
		}
		if n := bus.live(); n != 2 {
			t.Fatalf("%d subscriptions, want 2", n)
		}
	})
}

func TestNewRejectsBadArguments(t *testing.T) {
	forget := func(uint64) {}
	cases := map[string]struct {
		root   string
		forget func(uint64)
		log    *slog.Logger
	}{
		"empty root":     {"", forget, quiet},
		"dotted root":    {"live.x", forget, quiet},
		"wildcard root":  {"*", forget, quiet},
		"tail root":      {">", forget, quiet},
		"spaced root":    {"li ve", forget, quiet},
		"missing forget": {"live", nil, quiet},
		"missing log":    {"live", forget, nil},
	}
	for name, c := range cases {
		if _, err := newWatch(&fakeBus{}, c.root, c.forget, c.log); err == nil {
			t.Errorf("%s: newWatch succeeded, want an error", name)
		}
	}
	if _, err := New(nil, "live", forget, quiet); err == nil {
		t.Error("New with a nil connection succeeded, want an error")
	}
}
