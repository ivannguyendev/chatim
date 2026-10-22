package readbatch_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/readbatch"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestTwoAddsOfOneUserKeepTheLargerReadVer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t)
		rg.Add(roomA, "bob", pos(9, 5))
		rg.Add(roomA, "bob", pos(4, 3))
		rg.Add(roomA, "alice", pos(2, 1))
		rg.Add(roomA, "alice", pos(3, 2))
		rg.tick()
		got := rg.pub.snapshot()
		if len(got) != 1 || got[0].room != roomA.ID || len(got[0].events) != 1 {
			t.Fatalf("enqueued %+v, want one call for room %d with one event", got, roomA.ID)
		}
		if l := lines(got[0].events); !slices.Equal(l, []string{"alice:3:2", "bob:9:5"}) {
			t.Fatalf("entries = %v, want the larger read_ver per user sorted by user", l)
		}
		ev := got[0].events[0]
		if ev.GetTenant() != "acme" || ev.GetRoomId() != "101" || ev.GetActor() != "" || ev.GetThreadRoot() != 0 || ev.GetSeq() != 0 {
			t.Fatalf("envelope = %v, want room 101 of acme with no actor, thread or seq", ev)
		}
		if want := pbconv.ReadPositionsEventID(roomA.ID, ev.GetReadPositions().GetEntries()); ev.GetId() != want {
			t.Fatalf("id = %q, want %q", ev.GetId(), want)
		}
		rg.closeAndWait(t)
		rg.expectCounts(t, 4, 1, 0, 0)
	})
}

func TestRoomsFlushSeparately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t)
		rg.Add(roomA, "bob", pos(9, 5))
		rg.Add(roomB, "bob", pos(3, 2))
		rg.Add(roomB, "carol", pos(3, 1))
		rg.tick()
		got := rg.pub.byRoom()
		if l := lines(got[roomA.ID]); !slices.Equal(l, []string{"bob:9:5"}) {
			t.Fatalf("room A entries = %v", l)
		}
		if l := lines(got[roomB.ID]); !slices.Equal(l, []string{"bob:3:2", "carol:3:1"}) {
			t.Fatalf("room B entries = %v", l)
		}
		if ev := got[roomB.ID][0]; ev.GetTenant() != "beta" || ev.GetRoomId() != "202" {
			t.Fatalf("room B envelope = %v", ev)
		}
		if n := len(rg.pub.snapshot()); n != 2 {
			t.Fatalf("enqueue calls = %d, want one per room", n)
		}
		rg.closeAndWait(t)
	})
}

func TestALargeRoomSplitsIntoPagesOfAThousand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t)
		for i := range 2500 {
			rg.Add(roomA, user(2499-i), pos(uint64(i+1), 1))
		}
		rg.tick()
		got := rg.pub.snapshot()
		if len(got) != 1 {
			t.Fatalf("enqueue calls = %d, want one for the room", len(got))
		}
		var sizes []int
		for _, ev := range got[0].events {
			sizes = append(sizes, len(ev.GetReadPositions().GetEntries()))
		}
		if !slices.Equal(sizes, []int{1000, 1000, 500}) {
			t.Fatalf("page sizes = %v, want [1000 1000 500]", sizes)
		}
		all := entries(got[0].events)
		for i, e := range all {
			if e.GetUser() != user(i) {
				t.Fatalf("entry %d = %q, want %q: every user once, sorted across pages", i, e.GetUser(), user(i))
			}
		}
		rg.closeAndWait(t)
		rg.expectCounts(t, 2500, 3, 0, 0)
	})
}

func TestTheIDFollowsTheContent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t)
		rg.Add(roomA, "bob", pos(9, 5))
		rg.Add(roomA, "alice", pos(2, 1))
		rg.tick()
		rg.Add(roomA, "alice", pos(2, 1))
		rg.Add(roomA, "bob", pos(9, 5))
		rg.tick()
		rg.Add(roomA, "bob", pos(10, 6))
		rg.Add(roomA, "alice", pos(2, 1))
		rg.tick()
		got := rg.pub.byRoom()[roomA.ID]
		if len(got) != 3 {
			t.Fatalf("events = %d, want 3", len(got))
		}
		if got[0].GetId() != got[1].GetId() {
			t.Fatalf("same content got ids %q and %q", got[0].GetId(), got[1].GetId())
		}
		if got[2].GetId() == got[0].GetId() {
			t.Fatalf("new content kept the id %q", got[0].GetId())
		}
		rg.closeAndWait(t)
	})
}

func TestTheTickerFlushesOnlyRoomsWithData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t)
		rg.tick()
		if n := len(rg.pub.snapshot()); n != 0 {
			t.Fatalf("enqueue calls with nothing added = %d, want 0", n)
		}
		rg.Add(roomA, "bob", pos(9, 5))
		time.Sleep(window - time.Millisecond)
		synctest.Wait()
		if n := len(rg.pub.snapshot()); n != 0 {
			t.Fatalf("enqueue calls before the window = %d, want 0", n)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		rg.tick()
		rg.tick()
		if got := rg.pub.snapshot(); len(got) != 1 || got[0].room != roomA.ID {
			t.Fatalf("enqueued %+v, want room A once and no empty flush after", got)
		}
		rg.closeAndWait(t)
	})
}

func TestAddAfterCloseIsACountedDrop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t)
		rg.closeAndWait(t)
		rg.Add(roomA, "bob", pos(9, 5))
		rg.Add(roomB, "bob", pos(9, 5))
		rg.expectCounts(t, 0, 0, 2, 0)
		if err := rg.Close(context.Background()); err != nil {
			t.Fatalf("second Close = %v", err)
		}
		if n := len(rg.pub.snapshot()); n != 0 {
			t.Fatalf("enqueue calls = %d, want 0", n)
		}
	})
}

func TestCloseFlushesTheRemainderAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t)
		rg.Add(roomA, "bob", pos(9, 5))
		rg.Add(roomB, "carol", pos(1, 1))
		start := time.Now()
		rg.closeAndWait(t)
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("Close took %v, want no wait", elapsed)
		}
		if got := rg.pub.byRoom(); len(got) != 2 {
			t.Fatalf("flushed rooms = %d, want 2", len(got))
		}
		rg.expectCounts(t, 2, 2, 0, 0)
	})
}

func TestCloseWithoutRunFlushesTheRemainder(t *testing.T) {
	rg := newRig(t)
	rg.Add(roomA, "bob", pos(9, 5))
	if err := rg.Close(context.Background()); err != nil {
		t.Fatalf("Close before Run = %v", err)
	}
	if l := lines(rg.pub.byRoom()[roomA.ID]); !slices.Equal(l, []string{"bob:9:5"}) {
		t.Fatalf("entries = %v, want bob:9:5", l)
	}
	if err := rg.Run(context.Background()); err != nil {
		t.Fatalf("Run after Close = %v, want an immediate nil", err)
	}
}

func TestAnEnqueueErrorIsCountedAndNotRequeued(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t)
		rg.pub.setFail(errQueueFull)
		for i := range 1500 {
			rg.Add(roomA, user(i), pos(1, 1))
		}
		rg.tick()
		rg.pub.setFail(nil)
		rg.tick()
		if n := len(rg.pub.snapshot()); n != 1 {
			t.Fatalf("enqueue calls = %d, want the failed one only", n)
		}
		if n := rg.sink.Count(enqueueFailedMsg); n != 1 {
			t.Fatalf("logged %d failures, want 1", n)
		}
		rg.closeAndWait(t)
		rg.expectCounts(t, 1500, 0, 0, 2)
	})
}

func TestRunTwiceFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t)
		if err := rg.Run(context.Background()); err == nil {
			t.Fatal("second Run = nil, want an error")
		}
		rg.closeAndWait(t)
	})
}

func TestRunStopsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { rg.done <- rg.Run(ctx) }()
		rg.Add(roomA, "bob", pos(9, 5))
		cancel()
		if err := <-rg.done; err == nil {
			t.Fatal("Run after cancel = nil, want the context error")
		}
		if err := rg.Close(context.Background()); err != nil {
			t.Fatalf("Close = %v", err)
		}
		if n := len(rg.pub.snapshot()); n != 1 {
			t.Fatalf("enqueue calls = %d, want Close to flush the rest", n)
		}
	})
}

func TestNewChecksItsInputs(t *testing.T) {
	if _, err := readbatch.New(nil, readbatch.Config{}, nil); err == nil {
		t.Fatal("New(nil enqueue) = nil error")
	}
	cases := map[time.Duration]bool{
		0: true, 100 * time.Millisecond: true, 3 * time.Second: true,
		99 * time.Millisecond: false, 3*time.Second + time.Millisecond: false, -time.Second: false,
	}
	for w, ok := range cases {
		err := readbatch.Config{Window: w}.Validate()
		if (err == nil) != ok {
			t.Errorf("Validate(%v) = %v, want ok %v", w, err, ok)
		}
		if err != nil && !errorsIsInvalid(err) {
			t.Errorf("Validate(%v) = %v, want ErrInvalidArgument", w, err)
		}
	}
	if q, err := readbatch.New((&fakePublisher{}).Enqueue, readbatch.Config{Window: time.Hour}, nil); err == nil || q != nil {
		t.Fatalf("New(window 1h) = %v, %v; want an error", q, err)
	}
}

func errorsIsInvalid(err error) bool { return errors.Is(err, apperr.ErrInvalidArgument) }
