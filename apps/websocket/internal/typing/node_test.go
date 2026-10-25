package typing

import (
	"errors"
	"testing"
	"testing/synctest"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func startedFixture(t *testing.T) (*fixture, *fakeRelay, *fakeSink) {
	t.Helper()
	f := newFixture(t, 64)
	relay := &fakeRelay{}
	if err := f.svc.Start(relay); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := f.svc.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	f.member("b", 7)
	_, bSink := f.connect("b", "7")
	return f, relay, bSink
}

func TestStartSubscribesToEveryTypingSubject(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, relay, _ := startedFixture(t)
		if relay.subject != "eph.*.*.typing" {
			t.Fatalf("subscribed to %q, want eph.*.*.typing", relay.subject)
		}
	})
}

func TestASignalFromAnotherNodeReachesViewersLikeALocalOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, relay, bSink := startedFixture(t)
		relay.deliver("eph.t1.7.typing", signal(t, &chatimv1.TypingSignal{Origin: "ws-b:00ff00ff", Tenant: "t1", RoomId: 7, UserId: "a"}))
		synctest.Wait()
		got := bSink.notices()
		if len(got) != 1 || got[0].GetUserId() != "a" || got[0].GetRoomId() != "7" {
			t.Fatalf("b notices = %v, want one typing of a in 7", got)
		}
		relay.deliver("eph.t1.7.typing", signal(t, &chatimv1.TypingSignal{Origin: "ws-b:00ff00ff", Tenant: "t1", RoomId: 7, UserId: "b"}))
		synctest.Wait()
		if len(bSink.notices()) != 1 || f.counter("typing_delivered_total", "") != 1 {
			t.Fatal("a signal of the viewer itself reached the viewer")
		}
		if len(relay.published()) != 0 {
			t.Fatal("a signal from another node was published again")
		}
	})
}

func TestASignalFromThisNodeIsDroppedSilently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, relay, bSink := startedFixture(t)
		relay.deliver("eph.t1.7.typing", signal(t, &chatimv1.TypingSignal{Origin: testOrigin, Tenant: "t1", RoomId: 7, UserId: "a"}))
		synctest.Wait()
		if len(bSink.notices()) != 0 {
			t.Fatal("an echo of this node's own signal was delivered")
		}
		for _, reason := range dropLabels {
			if got := f.counter("typing_dropped_total", reason); got != 0 {
				t.Fatalf("drops %s = %v, want none for an echo", reason, got)
			}
		}
	})
}

func TestMalformedSignalsAreDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, relay, bSink := startedFixture(t)
		good := func(mut func(*chatimv1.TypingSignal)) []byte {
			sig := &chatimv1.TypingSignal{Origin: "ws-b:00ff00ff", Tenant: "t1", RoomId: 7, UserId: "a"}
			mut(sig)
			return signal(t, sig)
		}
		same := func(*chatimv1.TypingSignal) {}
		cases := []struct {
			subject string
			data    []byte
		}{
			{"eph.t1.7.typing", []byte{0xff, 0xff}},
			{"eph.t1.7", good(same)},
			{"eph.t1.7.typing.x", good(same)},
			{"eph.t2.7.typing", good(same)},
			{"eph.t1.8.typing", good(same)},
			{"eph.t1.x.typing", good(same)},
			{"eph.t1.7.typed", good(same)},
			{"evt.t1.7.typing", good(same)},
			{"eph.T1.7.typing", good(func(s *chatimv1.TypingSignal) { s.Tenant = "T1" })},
			{"eph.t1.7.typing", good(func(s *chatimv1.TypingSignal) { s.UserId = "" })},
			{"eph.t1.7.typing", good(func(s *chatimv1.TypingSignal) { s.UserId = "a b" })},
		}
		for _, c := range cases {
			relay.deliver(c.subject, c.data)
		}
		synctest.Wait()
		if len(bSink.notices()) != 0 {
			t.Fatalf("malformed signals delivered: %v", bSink.notices())
		}
		if got := f.counter("typing_dropped_total", "malformed"); got != float64(len(cases)) {
			t.Fatalf("malformed drops = %v, want %d", got, len(cases))
		}
	})
}

func TestAFullQueueDropsSignalsAsQueueFull(t *testing.T) {
	f := newFixture(t, 2)
	for range 5 {
		f.svc.FromNode("eph.t1.7.typing", nil)
	}
	if got := f.counter("typing_dropped_total", "queue_full"); got != 3 {
		t.Fatalf("queue_full drops = %v, want 3", got)
	}
}

func TestStartAndStopLeaveNoGoroutineAndStopUnsubscribes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, 2)
		if err := f.svc.Stop(); err != nil {
			t.Fatalf("Stop before Start: %v", err)
		}
		if err := f.svc.Start(nil); err == nil {
			t.Fatal("Start accepted a nil relay")
		}
		if err := f.svc.Start(&fakeRelay{subscribeErr: errors.New("nats closed")}); err == nil {
			t.Fatal("Start ignored a subscribe error")
		}
		relay := &fakeRelay{unsubErr: errors.New("connection closed")}
		if err := f.svc.Start(relay); err != nil {
			t.Fatalf("Start after a failed one: %v", err)
		}
		if err := f.svc.Start(relay); err == nil {
			t.Fatal("Start ran twice")
		}
		if err := f.svc.Stop(); err == nil {
			t.Fatal("Stop hid the unsubscribe error")
		}
		if err := f.svc.Stop(); err != nil || relay.unsubscribed != 1 {
			t.Fatalf("second Stop = %v, unsubscribes %d, want nil and 1", err, relay.unsubscribed)
		}
		f.member("a", 7)
		a, _ := f.connect("a")
		f.svc.FromClient(a, "t1", "a", typingFrame("7", false))
		if len(relay.published()) != 0 || f.counter("typing_relay_failures_total", "") != 0 {
			t.Fatal("a typing frame after Stop was published")
		}
	})
}
