package typing

import (
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestNewRefusesMissingParts(t *testing.T) {
	table := newFixture(t, 1).table
	for name, tc := range map[string]struct {
		cfg     Config
		viewers Viewers
	}{
		"no viewers": {Config{Origin: testOrigin, Queue: 1}, nil},
		"no origin":  {Config{Queue: 1}, table},
		"no queue":   {Config{Origin: testOrigin}, table},
	} {
		if _, err := New(tc.cfg, tc.viewers); err == nil {
			t.Errorf("%s: New accepted %+v", name, tc.cfg)
		}
	}
}

func TestNewOriginIsTheNodeIDWithEightRandomHexChars(t *testing.T) {
	a, err := NewOrigin("ws-a")
	if err != nil {
		t.Fatalf("NewOrigin: %v", err)
	}
	b, _ := NewOrigin("ws-a")
	suffix, ok := strings.CutPrefix(a, "ws-a:")
	if !ok || len(suffix) != 8 || strings.Trim(suffix, "0123456789abcdef") != "" || a == b {
		t.Fatalf("origins %q and %q, want ws-a: plus 8 distinct hex chars", a, b)
	}
}

func TestFromClientReachesOtherViewersOnceAndPublishesOneSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, 8)
		relay := &fakeRelay{}
		if err := f.svc.Start(relay); err != nil {
			t.Fatalf("Start: %v", err)
		}
		for _, u := range []string{"a", "b", "c"} {
			f.member(u, 7)
		}
		a, aSink := f.connect("a", "7")
		_, a2Sink := f.connect("a", "7")
		_, bSink := f.connect("b", "7")
		_, cSink := f.connect("c")
		f.svc.FromClient(a, "t1", "a", typingFrame("7", false))
		got := bSink.notices()
		if len(got) != 1 || got[0].GetRoomId() != "7" || got[0].GetUserId() != "a" || got[0].GetStopped() {
			t.Fatalf("b notices = %v, want one typing of a in 7", got)
		}
		if len(aSink.notices())+len(a2Sink.notices())+len(cSink.notices()) != 0 {
			t.Fatal("the sender's devices or a member without the room open got a notice")
		}
		sent := relay.published()
		if len(sent) != 1 || sent[0].subject != "eph.t1.7.typing" {
			t.Fatalf("published = %v, want one signal on eph.t1.7.typing", sent)
		}
		var sig chatimv1.TypingSignal
		if err := proto.Unmarshal(sent[0].data, &sig); err != nil {
			t.Fatalf("decode signal: %v", err)
		}
		if sig.GetOrigin() != testOrigin || sig.GetTenant() != "t1" || sig.GetRoomId() != 7 || sig.GetUserId() != "a" || sig.GetStopped() {
			t.Fatalf("signal = %v", &sig)
		}
		if f.counter("typing_received_total", "") != 1 || f.counter("typing_delivered_total", "") != 1 {
			t.Fatalf("received %v delivered %v, want 1 and 1", f.counter("typing_received_total", ""), f.counter("typing_delivered_total", ""))
		}
		if err := f.svc.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})
}

func TestFromClientDropsANonMemberOrAMissingConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, 8)
		relay := &fakeRelay{}
		if err := f.svc.Start(relay); err != nil {
			t.Fatalf("Start: %v", err)
		}
		f.member("b", 7)
		f.member("a", 8)
		a, _ := f.connect("a")
		_, bSink := f.connect("b", "7")
		f.svc.FromClient(a, "t1", "a", typingFrame("7", false))
		f.svc.FromClient(nil, "t1", "a", typingFrame("7", false))
		f.svc.FromClient(a, "t1", "a", typingFrame("bad", false))
		if len(bSink.notices()) != 0 || len(relay.published()) != 0 {
			t.Fatal("a non-member typing reached a viewer or the relay")
		}
		if got := f.counter("typing_dropped_total", "not_member"); got != 3 {
			t.Fatalf("not_member drops = %v, want 3", got)
		}
		if err := f.svc.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})
}

func TestAFailedPublishStillDeliversLocallyAndIsCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, 8)
		relay := &fakeRelay{publishErr: errors.New("nats not connected")}
		if err := f.svc.Start(relay); err != nil {
			t.Fatalf("Start: %v", err)
		}
		f.member("a", 7)
		f.member("b", 7)
		a, _ := f.connect("a")
		_, bSink := f.connect("b", "7")
		f.svc.FromClient(a, "t1", "a", typingFrame("7", true))
		if got := bSink.notices(); len(got) != 1 || !got[0].GetStopped() {
			t.Fatalf("b notices = %v, want one stopped notice", got)
		}
		if got := f.counter("typing_relay_failures_total", ""); got != 1 {
			t.Fatalf("relay failures = %v, want 1", got)
		}
		if err := f.svc.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})
}

func TestAFullViewerQueueIsCountedAsSendFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, 8)
		f.member("a", 7)
		f.member("b", 7)
		a, _ := f.connect("a")
		_, bSink := f.connect("b", "7")
		bSink.mu.Lock()
		bSink.refuse = true
		bSink.mu.Unlock()
		f.svc.FromClient(a, "t1", "a", typingFrame("7", false))
		if f.counter("typing_dropped_total", "send_full") != 1 || f.counter("typing_delivered_total", "") != 0 {
			t.Fatal("a refused offer was not counted as send_full")
		}
	})
}

func TestRateLimitedCountsARateDrop(t *testing.T) {
	f := newFixture(t, 1)
	f.svc.RateLimited()
	if f.counter("typing_dropped_total", "rate") != 1 || f.counter("typing_received_total", "") != 1 {
		t.Fatal("RateLimited did not count a received frame dropped for rate")
	}
}
