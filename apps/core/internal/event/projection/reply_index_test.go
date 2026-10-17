package projection_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func repliesEventID(parent, ver uint64) string {
	return pbconv.MessageCountsEventID(roomA, 0, parent, pbconv.RepliesCounter, ver)
}

func TestAReplyDeliveredTwiceIsCountedOnce(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	ev := rg.send(t, 2, replyTo(1))
	for range 2 {
		noErrors(t, rg.handle(t, ev), 1)
	}
	if got := rg.message(t, 1).Replies; got != (domain.ReplyCount{N: 1, Version: 1}) || rg.liveReplies(t, 1) != 1 {
		t.Fatalf("rc = %+v, live replies %d; want {1 1} and one reply doc", got, rg.liveReplies(t, 1))
	}
	parent := store.MsgKey{Room: roomA, Seq: 1}
	want := []armedCheck{{key: parent, counter: pbconv.RepliesCounter}, {key: parent, counter: pbconv.RepliesCounter}}
	if armed, disarmed := rg.timers.checks(); !slices.Equal(armed, want) || !slices.Equal(disarmed, []uint64{1, 2}) {
		t.Fatalf("armed %v disarmed %v; want a timer per delivery, each removed", armed, disarmed)
	}
	if got := rg.queue.ids(); !slices.Equal(got, []string{repliesEventID(1, 1)}) {
		t.Fatalf("enqueued %v, want one counts_changed for v1", got)
	}
	threads, err := rg.threads.List(t.Context(), roomA, store.ReplyThreadCursor{}, 10)
	if err != nil || len(threads) != 1 || threads[0].Parent != parent || threads[0].LastReplySeq != 2 {
		t.Fatalf("reply threads = %+v, %v; want parent 1 touched by reply 2", threads, err)
	}
}

func TestADeletedReplyFlipsOnce(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	noErrors(t, rg.handle(t, rg.send(t, 2, replyTo(1))), 1)
	del := rg.edit(t, 2, 1, domain.EditDelete)
	for range 2 {
		noErrors(t, rg.handle(t, del), 1)
	}
	if got := rg.message(t, 1).Replies; got != (domain.ReplyCount{N: 0, Version: 2}) || rg.liveReplies(t, 1) != 0 {
		t.Fatalf("rc = %+v live %d; want {0 2} and no live reply", got, rg.liveReplies(t, 1))
	}
	if got := rg.queue.ids(); !slices.Equal(got, []string{repliesEventID(1, 1), repliesEventID(1, 2)}) {
		t.Fatalf("enqueued %v, want counts_changed v1 then v2", got)
	}
	if armed, disarmed := rg.timers.checks(); len(armed) != 3 || !slices.Equal(disarmed, []uint64{1, 2, 3}) {
		t.Fatalf("armed %v disarmed %v; want every timer removed", armed, disarmed)
	}
}

func TestADeleteDeliveredBeforeItsReplyIsNeverCounted(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	created := rg.send(t, 2, replyTo(1))
	deleted := rg.edit(t, 2, 1, domain.EditDelete)
	noErrors(t, rg.handle(t, deleted, created), 2)
	if got := rg.message(t, 1).Replies; got != (domain.ReplyCount{}) || rg.liveReplies(t, 1) != 0 || len(rg.queue.ids()) != 0 {
		t.Fatalf("rc %+v live %d events %v; want nothing counted", got, rg.liveReplies(t, 1), rg.queue.ids())
	}
}

func TestACountFailureKeepsTheTimerAndNaks(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	ev := rg.send(t, 2, replyTo(1))
	d := rg.deps()
	d.Counts = failingReplyCount{err: errBoom}
	if errs := rg.build(t, d).Handle(t.Context(), []projection.Event{ev}); !errors.Is(errs[0], errBoom) || eventkit.Gone(errs[0]) {
		t.Fatalf("errs = %v, want the count failure so the event is Nak'd", errs)
	}
	if _, disarmed := rg.timers.checks(); rg.liveReplies(t, 1) != 1 || len(disarmed) != 0 || rg.message(t, 1).Replies != (domain.ReplyCount{}) {
		t.Fatalf("live %d disarmed %v rc %+v; want the doc written, the timer kept, no count", rg.liveReplies(t, 1), disarmed, rg.message(t, 1).Replies)
	}
	noErrors(t, rg.handle(t, ev), 1)
	if _, disarmed := rg.timers.checks(); rg.message(t, 1).Replies != (domain.ReplyCount{}) || !slices.Equal(disarmed, []uint64{2}) {
		t.Fatalf("rc %+v disarmed %v; want no second count and only the new timer removed (timer 1 repairs)", rg.message(t, 1).Replies, disarmed)
	}
}

func TestAFailedArmWritesNothing(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	rg.timers.err = errBoom
	if errs := rg.handle(t, rg.send(t, 2, replyTo(1))); !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want the arm failure", errs)
	}
	threads, err := rg.threads.List(t.Context(), roomA, store.ReplyThreadCursor{}, 10)
	if rg.liveReplies(t, 1) != 0 || err != nil || len(threads) != 0 || len(rg.queue.ids()) != 0 {
		t.Fatalf("live %d threads %v (%v) events %v; want nothing written before a timer exists", rg.liveReplies(t, 1), threads, err, rg.queue.ids())
	}
}

func TestAVanishedParentDropsTheEvent(t *testing.T) {
	rg := newIndexRig(t)
	if errs := rg.handle(t, rg.send(t, 2, replyTo(1))); !eventkit.Gone(errs[0]) {
		t.Fatalf("errs = %v, want a gone error so the event is acked and counted as dropped", errs)
	}
	if _, disarmed := rg.timers.checks(); !slices.Equal(disarmed, []uint64{1}) {
		t.Fatalf("disarmed %v, want the timer removed", disarmed)
	}
}

func TestAFullEventQueueStillDisarmsBecauseTheParentFlagRepublishes(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	rg.queue.err = errBoom
	noErrors(t, rg.handle(t, rg.send(t, 2, replyTo(1))), 1)
	if _, disarmed := rg.timers.checks(); rg.message(t, 1).Replies != (domain.ReplyCount{N: 1, Version: 1}) || !slices.Equal(disarmed, []uint64{1}) {
		t.Fatalf("rc %+v disarmed %v; want the count kept and the timer removed", rg.message(t, 1).Replies, disarmed)
	}
}

func TestAReplyEventWithoutARoomTypeIsCountedWithoutAnEvent(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	ev := rg.send(t, 2, replyTo(1))
	ev.Proto.RoomType = 0
	noErrors(t, rg.handle(t, ev), 1)
	if rg.message(t, 1).Replies != (domain.ReplyCount{N: 1, Version: 1}) || len(rg.queue.ids()) != 0 {
		t.Fatalf("rc %+v events %v; want the count and no event", rg.message(t, 1).Replies, rg.queue.ids())
	}
}
