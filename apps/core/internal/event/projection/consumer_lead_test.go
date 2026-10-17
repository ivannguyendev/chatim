package projection_test

import (
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
)

const (
	leadTick       = time.Second
	leadStartedMsg = "projection started on the leader slot"
	leadLostMsg    = "projection lost the leader slot; fetching stopped"
)

type fakeOwner struct{ owns atomic.Bool }

func (o *fakeOwner) Owns(slot uint16) bool { return slot == 0 && o.owns.Load() }

func (s *fakeSource) asks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.asked)
}

func newLeadRig(t *testing.T, owner *fakeOwner) *consumerRig {
	t.Helper()
	rg := &consumerRig{sink: &testlog.Sink{}, src: newFakeSource(), writer: &fakeWriter{}, result: make(chan error, 1)}
	lead := projection.WithLeader(owner, 0, leadTick)
	rg.c = built(projection.NewConsumer([]projection.Projection{projection.NewRoomActivity(rg.writer)}, rg.src, evtConfig, rg.sink.Logger(), lead))(t)
	go func() { rg.result <- rg.c.Run(t.Context()) }()
	synctest.Wait()
	return rg
}

func TestLeaderGatedConsumerNeverFetchesWithoutTheLeaderSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := &fakeOwner{}
		rg := newLeadRig(t, owner)
		time.Sleep(10 * leadTick)
		synctest.Wait()
		if n := rg.src.asks(); n != 0 {
			t.Fatalf("a core without slot 0 fetched %d times, want never", n)
		}
		owner.owns.Store(true)
		time.Sleep(leadTick)
		synctest.Wait()
		d := delivery(t, msgCreated(roomA, 0, 1, at(1)), 1)
		rg.src.push(d)
		synctest.Wait()
		if acks, _, _ := d.settled(); acks != 1 || len(rg.writer.touched()) != 1 {
			t.Fatalf("after taking the lead: acks %d, writes %d; want the event written and acked", acks, len(rg.writer.touched()))
		}
		if n := rg.sink.Count(leadStartedMsg); n != 1 {
			t.Errorf("logged the lead start %d times, want 1", n)
		}
		rg.stop(t)
	})
}

func TestLeaderGatedConsumerNaksWhatItHoldsWhenTheLeadIsLostAndStopsFetching(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := &fakeOwner{}
		owner.owns.Store(true)
		rg := newLeadRig(t, owner)
		if n := rg.src.asks(); n != 1 {
			t.Fatalf("leader fetched %d times, want one fetch waiting", n)
		}
		owner.owns.Store(false)
		ds := []*fakeDelivery{
			delivery(t, msgCreated(roomA, 0, 1, at(1)), 1),
			delivery(t, msgCreated(roomB, 0, 1, at(1)), 1),
		}
		rg.src.push(ds...)
		synctest.Wait()
		for i, d := range ds {
			if acks, _, naks := d.settled(); acks != 0 || !slices.Equal(naks, []time.Duration{0}) {
				t.Errorf("delivery %d: acks %d naks %v; want one Nak(0)", i, acks, naks)
			}
		}
		if got := rg.writer.touched(); len(got) != 0 {
			t.Fatalf("wrote %v after losing the lead, want nothing", got)
		}
		time.Sleep(10 * leadTick)
		synctest.Wait()
		if n := rg.src.asks(); n != 1 {
			t.Fatalf("fetched %d times after losing the lead, want no new fetch", n)
		}
		if n := rg.sink.Count(leadLostMsg); n != 1 {
			t.Errorf("logged the lost lead %d times, want 1", n)
		}
		owner.owns.Store(true)
		time.Sleep(leadTick)
		synctest.Wait()
		if n := rg.src.asks(); n != 2 {
			t.Fatalf("fetched %d times after the lead came back, want 2", n)
		}
		rg.stop(t)
	})
}

func TestLeaderGatedConsumerFinishesTheBatchItIsWritingWhenTheLeadIsLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := &fakeOwner{}
		owner.owns.Store(true)
		rg := newLeadRig(t, owner)
		held, release := rg.writer.holdNext()
		d := delivery(t, msgCreated(roomA, 0, 1, at(1)), 1)
		rg.src.push(d)
		<-held
		owner.owns.Store(false)
		close(release)
		synctest.Wait()
		if acks, _, naks := d.settled(); acks != 1 || len(naks) != 0 {
			t.Fatalf("acks %d naks %v; want the batch in hand finished and acked", acks, naks)
		}
		time.Sleep(10 * leadTick)
		synctest.Wait()
		if n := rg.src.asks(); n != 1 {
			t.Fatalf("fetched %d times after losing the lead, want 1", n)
		}
		rg.stop(t)
	})
}

func TestLeaderOptionNeedsATickAndANilOwnerAlwaysFetches(t *testing.T) {
	p, src := []projection.Projection{projection.NewRoomActivity(&fakeWriter{})}, newFakeSource()
	if _, err := projection.NewConsumer(p, src, evtConfig, nil, projection.WithLeader(&fakeOwner{}, 0, 0)); err == nil {
		t.Fatal("NewConsumer with a zero leader tick = nil error")
	}
	synctest.Test(t, func(t *testing.T) {
		src := newFakeSource()
		c := built(projection.NewConsumer(p, src, evtConfig, nil, projection.WithLeader(nil, 0, 0)))(t)
		done := make(chan error, 1)
		go func() { done <- c.Run(t.Context()) }()
		synctest.Wait()
		if n := src.asks(); n != 1 {
			t.Errorf("consumer without an owner fetched %d times, want 1", n)
		}
		if err := c.Close(t.Context()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		<-done
	})
}
