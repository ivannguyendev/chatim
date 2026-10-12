package effects_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestReplyMentionIndexRunsAtOnce(t *testing.T) {
	if e := newIndexRig(t).index.Effect(); e.Name != effects.ReplyMentionIndexName || e.Delay != 0 {
		t.Fatalf("effect = %q delay %v, want %q delay 0", e.Name, e.Delay, effects.ReplyMentionIndexName)
	}
}

func TestAReplyIsCountedOnceAcrossRedeliveries(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	rec := rg.send(t, 2, replyTo(1))
	for range 2 {
		if errs := rg.run(t, rec); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	if got := rg.replies(t, 1); got != (domain.ReplyCount{N: 1, Version: 1}) || rg.liveReplies(t, 1) != 1 {
		t.Fatalf("rc = %+v, live replies %d; want {1 1} and one reply doc", got, rg.liveReplies(t, 1))
	}
	parent := store.MsgKey{Room: room, Seq: 1}
	want := []armedCheck{{key: parent, counter: pbconv.RepliesCounter}, {key: parent, counter: pbconv.RepliesCounter}}
	if armed, disarmed := rg.timers.checks(), rg.timers.disarms(); !slices.Equal(armed, want) || !slices.Equal(disarmed, []uint64{1, 2}) {
		t.Fatalf("armed %v disarmed %v; want a timer per delivery, each removed", armed, disarmed)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MessageCountsEventID(room, 0, 1, pbconv.RepliesCounter, 1)}) || rg.index.Republished() != 1 {
		t.Fatalf("stored %v republished %d; want one counts_changed for v1", got, rg.index.Republished())
	}
}

func TestACountFailureAfterTheReplyDocKeepsTheTimer(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	rec := rg.send(t, 2, replyTo(1))
	d := rg.deps()
	d.Counts = failingReplyCount{err: errBoom}
	if errs := rg.build(t, d).Effect().Run(t.Context(), []work.Record{rec}); !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want the count failure so the record is Nak'd", errs)
	}
	if rg.liveReplies(t, 1) != 1 || len(rg.timers.disarms()) != 0 || rg.replies(t, 1) != (domain.ReplyCount{}) {
		t.Fatalf("live %d disarmed %v rc %+v; want the doc written, the timer kept, no count", rg.liveReplies(t, 1), rg.timers.disarms(), rg.replies(t, 1))
	}
	if errs := rg.run(t, rec); !allNil(errs, 1) {
		t.Fatalf("redelivery errs = %v", errs)
	}
	if rg.replies(t, 1) != (domain.ReplyCount{}) || !slices.Equal(rg.timers.disarms(), []uint64{2}) {
		t.Fatalf("rc %+v disarmed %v; want no second count and only the new timer removed (timer 1 repairs)", rg.replies(t, 1), rg.timers.disarms())
	}
}

func TestADeletedReplyIsUncountedOnce(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	if errs := rg.run(t, rg.send(t, 2, replyTo(1))); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	del := rg.edit(t, 2, 1, domain.EditDelete)
	for range 2 {
		if errs := rg.run(t, del); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	if got := rg.replies(t, 1); got != (domain.ReplyCount{N: 0, Version: 2}) || rg.liveReplies(t, 1) != 0 {
		t.Fatalf("rc = %+v live %d; want {0 2}", got, rg.liveReplies(t, 1))
	}
	want := []string{pbconv.MessageCountsEventID(room, 0, 1, pbconv.RepliesCounter, 1), pbconv.MessageCountsEventID(room, 0, 1, pbconv.RepliesCounter, 2)}
	if got := storedEventIDs(rg.js); !slices.Equal(got, want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
}

func TestAReplyDeletedBeforeItsRecordIsNeverCounted(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	ins := rg.send(t, 2, replyTo(1))
	del := rg.edit(t, 2, 1, domain.EditDelete)
	if errs := rg.run(t, ins); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if errs := rg.run(t, del); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := rg.replies(t, 1); got != (domain.ReplyCount{}) || rg.liveReplies(t, 1) != 0 || len(rg.js.Stored()) != 0 {
		t.Fatalf("rc %+v live %d events %d; want nothing counted", got, rg.liveReplies(t, 1), len(rg.js.Stored()))
	}
}

func TestAnEditThatKeepsTheReplyLeavesTheCountAlone(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	if errs := rg.run(t, rg.send(t, 2, replyTo(1)), rg.edit(t, 2, 1, domain.EditText)); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	if got := rg.replies(t, 1); got != (domain.ReplyCount{N: 1, Version: 1}) || len(rg.timers.checks()) != 1 {
		t.Fatalf("rc %+v armed %v; want only the insert to count", got, rg.timers.checks())
	}
}

func TestAVanishedParentDropsTheCount(t *testing.T) {
	rg := newIndexRig(t)
	rec := rg.send(t, 2, replyTo(1))
	if errs := rg.run(t, rec); !allNil(errs, 1) {
		t.Fatalf("errs = %v, want the record acked", errs)
	}
	if rg.index.Dropped() != 1 || !slices.Equal(rg.timers.disarms(), []uint64{1}) {
		t.Fatalf("dropped %d disarmed %v; want one drop and the timer removed", rg.index.Dropped(), rg.timers.disarms())
	}
}

func TestATimerFailureWritesNothing(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	rg.timers.err = errBoom
	if errs := rg.run(t, rg.send(t, 2, replyTo(1))); !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want the arm failure", errs)
	}
	if rg.liveReplies(t, 1) != 0 {
		t.Fatalf("live replies %d, want none before a timer exists", rg.liveReplies(t, 1))
	}
}
