package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type failingThreads struct{ err error }

func (f failingThreads) Touch(context.Context, store.MsgKey, string, uint64, time.Time) error {
	return f.err
}

func (rg *indexRig) threadsOf(t *testing.T) []store.ReplyThread {
	t.Helper()
	got, err := rg.threads.List(t.Context(), room, store.ReplyThreadCursor{}, 10)
	if err != nil {
		t.Fatalf("List threads: %v", err)
	}
	return got
}

func assertThread(t *testing.T, got []store.ReplyThread, parent, lastSeq uint64, at time.Time) {
	t.Helper()
	if len(got) != 1 || got[0].Parent != (store.MsgKey{Room: room, Seq: parent}) || got[0].LastReplySeq != lastSeq || !got[0].LastReplyAt.Equal(at) || got[0].Tenant != tenant {
		t.Fatalf("threads = %+v, want parent %d with last reply %d at %v", got, parent, lastSeq, at)
	}
}

func TestEveryLiveReplyRecordTouchesItsThread(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	first := rg.send(t, 2, replyTo(1))
	later := rg.send(t, 3, func(m *domain.Message) { m.ReplyTo, m.CreatedAt = &domain.ReplyRef{Seq: 1}, indexedAt.Add(time.Minute) })
	if errs := rg.run(t, first, later, first); !allNil(errs, 3) {
		t.Fatalf("errs = %v", errs)
	}
	assertThread(t, rg.threadsOf(t), 1, 3, indexedAt.Add(time.Minute))
}

func TestAThreadIsWrittenWhenTheReplyDocAlreadyExists(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	rec := rg.send(t, 2, replyTo(1))
	reply := domain.Reply{Parent: domain.MsgKey{Room: room, Seq: 1}, Room: room, Seq: 2, Tenant: tenant, From: "alice", At: indexedAt}
	if added, err := rg.inter.AddReply(t.Context(), reply); err != nil || !added {
		t.Fatalf("AddReply = %v, %v", added, err)
	}
	if errs := rg.run(t, rec); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	assertThread(t, rg.threadsOf(t), 1, 2, indexedAt)
	if !slices.Equal(rg.timers.disarms(), []uint64{1}) || len(rg.js.Stored()) != 0 {
		t.Fatalf("disarmed %v stored %d; want the timer removed and no count event", rg.timers.disarms(), len(rg.js.Stored()))
	}
}

func TestADeletedReplyLeavesItsThreadAlone(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	if errs := rg.run(t, rg.send(t, 2, replyTo(1))); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if errs := rg.run(t, rg.edit(t, 2, 1, domain.EditDelete)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	assertThread(t, rg.threadsOf(t), 1, 2, indexedAt)
}

func TestAThreadFailureNaksAfterTheCountSettles(t *testing.T) {
	rg := newIndexRig(t)
	rg.send(t, 1, nil)
	rec := rg.send(t, 2, replyTo(1))
	d := rg.deps()
	d.Threads = failingThreads{err: errBoom}
	if errs := rg.build(t, d).Effect().Run(t.Context(), []work.Record{rec}); !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want the thread failure so the record is Nak'd", errs)
	}
	if got := rg.replies(t, 1); got != (domain.ReplyCount{N: 1, Version: 1}) || !slices.Equal(rg.timers.disarms(), []uint64{1}) {
		t.Fatalf("rc %+v disarmed %v; want the count done and its timer removed before the thread write", got, rg.timers.disarms())
	}
	if errs := rg.run(t, rec); !allNil(errs, 1) {
		t.Fatalf("redelivery errs = %v", errs)
	}
	assertThread(t, rg.threadsOf(t), 1, 2, indexedAt)
	if got := rg.replies(t, 1); got != (domain.ReplyCount{N: 1, Version: 1}) {
		t.Fatalf("rc after redelivery = %+v, want still counted once", got)
	}
}
