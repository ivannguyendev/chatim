package interest

import (
	"slices"
	"strconv"
	"testing"
	"testing/synctest"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func typingFrame(t *testing.T, room uint64, user string) []byte {
	t.Helper()
	notice := &chatimv1.TypingNotice{RoomId: strconv.FormatUint(room, 10), UserId: user}
	raw, err := proto.Marshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Typing{Typing: notice}})
	if err != nil {
		t.Fatalf("marshal typing: %v", err)
	}
	return raw
}

func refuseOffers(s *fakeSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuseOffers = true
}

func eventStats(f *fixture) [3]uint64 {
	return [3]uint64{f.table.stats.delivered[levelMember].Load(), f.table.stats.delivered[levelOpen].Load(), f.table.stats.delivered[levelOwner].Load()}
}

func TestSignalRoomIsOkOnlyForALiveMemberOfTheRoom(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		c := f.table.Register("t1", "u", &fakeSink{})
		synctest.Wait()
		if _, ok := c.SignalRoom("1"); ok {
			t.Fatal("signal allowed before ready")
		}
		f.loader.answer(0, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		if room, ok := c.SignalRoom("1"); !ok || room != 1 {
			t.Fatalf("signal room = %d, %v, want 1, true", room, ok)
		}
		for _, raw := range []string{"2", "abc", "", "0"} {
			if _, ok := c.SignalRoom(raw); ok {
				t.Fatalf("signal allowed for room %q", raw)
			}
		}
		f.route(removed("t1", 1, "u", 2))
		if _, ok := c.SignalRoom("1"); ok {
			t.Fatal("signal allowed after member_removed")
		}
	})
}

func TestSignalRoomIsRefusedWhileSuspendedAndAfterUnregister(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, _ := f.connect("t1", "u", 1)
		f.loader.holding(true)
		f.table.Jumped(lagJump(f.seq))
		synctest.Wait()
		if _, ok := c.SignalRoom("1"); ok {
			t.Fatal("signal allowed while suspended")
		}
		f.loader.answer(1, reply{rooms: []Room{{ID: 1, Ver: 1}}})
		synctest.Wait()
		if _, ok := c.SignalRoom("1"); !ok {
			t.Fatal("signal refused after the reload")
		}
		c.Unregister()
		if _, ok := c.SignalRoom("1"); ok {
			t.Fatal("signal allowed after unregister")
		}
	})
}

func TestSignalReachesEveryViewerButTheSenderAndCountsRefusedOffers(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		lan, lanSink := f.connect("t1", "lan", 1)
		lan2, lan2Sink := f.connect("t1", "lan", 1)
		hoa, hoaSink := f.connect("t1", "hoa", 1)
		hoa2, hoa2Sink := f.connect("t1", "hoa", 1)
		_, namSink := f.connect("t1", "nam", 1)
		an, anSink := f.connect("t1", "an", 1)
		other, otherSink := f.connect("t2", "hoa", 1)
		for _, c := range []*Conn{lan, lan2, hoa, hoa2, an, other} {
			c.Open("1")
		}
		sinks := []*fakeSink{lanSink, lan2Sink, hoaSink, hoa2Sink, namSink, anSink, otherSink}
		for _, s := range sinks {
			s.take()
		}
		refuseOffers(anSink)
		before := eventStats(f)
		delivered, dropped := f.table.Signal("t1", 1, typingFrame(t, 1, "lan"), "lan")
		if delivered != 2 || dropped != 1 {
			t.Fatalf("delivered %d dropped %d, want 2 and 1", delivered, dropped)
		}
		if got := eventStats(f); got != before {
			t.Fatalf("event stats moved from %v to %v", before, got)
		}
		if anSink.offerCount() != 1 || lanSink.offerCount() != 0 || lan2Sink.offerCount() != 0 {
			t.Fatalf("offers an %d lan %d lan2 %d", anSink.offerCount(), lanSink.offerCount(), lan2Sink.offerCount())
		}
		for _, s := range []*fakeSink{hoaSink, hoa2Sink} {
			sends, trySends := s.counts()
			if got := s.take(); !slices.Equal(got, []string{"typing 1 lan"}) || sends != 0 || trySends != 0 {
				t.Fatalf("viewer frames %v sends %d try sends %d", got, sends, trySends)
			}
		}
		for _, s := range sinks {
			if got := s.take(); len(got) != 0 || s.failures() != 0 {
				t.Fatalf("frames %v failures %d", got, s.failures())
			}
		}
		if delivered, dropped := f.table.Signal("t1", 2, typingFrame(t, 2, "lan"), "lan"); delivered != 0 || dropped != 0 {
			t.Fatalf("a room nobody views: delivered %d dropped %d", delivered, dropped)
		}
	})
}

func TestSignalSkipsABackloggedViewerWithoutCountingADrop(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		slow, slowSink := f.connect("t1", "slow", 1)
		fast, fastSink := f.connect("t1", "fast", 1)
		slow.Open("1")
		fast.Open("1")
		slowSink.refusing(refuseEvents)
		f.route(catchingUp(msg("t1", 1)))
		if got := slowSink.take(); !slices.Equal(got, []string{"ready", "resync client_backlog"}) {
			t.Fatalf("slow device frames = %v", got)
		}
		fastSink.take()
		slowSink.refusing(acceptAll)
		delivered, dropped := f.table.Signal("t1", 1, typingFrame(t, 1, "lan"), "lan")
		if delivered != 1 || dropped != 0 || slowSink.offerCount() != 0 {
			t.Fatalf("delivered %d dropped %d slow offers %d", delivered, dropped, slowSink.offerCount())
		}
		if got := fastSink.take(); !slices.Equal(got, []string{"typing 1 lan"}) {
			t.Fatalf("fast device frames = %v", got)
		}
		if got := slowSink.take(); len(got) != 0 {
			t.Fatalf("a backlogged device got %v", got)
		}
	})
}

func TestSignalSkipsTheViewersOfASuspendedUser(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		c, s := f.connect("t1", "u", 1)
		c.Open("1")
		s.take()
		f.loader.holding(true)
		f.table.Jumped(lagJump(f.seq))
		synctest.Wait()
		if delivered, dropped := f.table.Signal("t1", 1, typingFrame(t, 1, "lan"), "lan"); delivered != 0 || dropped != 0 || s.offerCount() != 0 {
			t.Fatalf("suspended viewer: delivered %d dropped %d offers %d", delivered, dropped, s.offerCount())
		}
	})
}
