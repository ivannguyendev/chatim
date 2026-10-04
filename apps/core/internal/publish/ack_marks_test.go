package publish_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	markFailedMsg = "marking acked events failed; reconciliation republishes them"
	markWindow    = 10 * time.Millisecond
	markBatch     = 256
)

type recordingMarker struct {
	mu    sync.Mutex
	keys  []store.MsgKey
	sizes []int
	err   error
}

func (m *recordingMarker) Mark(_ context.Context, keys []store.MsgKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys = append(m.keys, keys...)
	m.sizes = append(m.sizes, len(keys))
	return m.err
}

func (m *recordingMarker) calls() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sizes)
}

func seqs(from, to uint64) []uint64 {
	var out []uint64
	for s := from; s <= to; s++ {
		out = append(out, s)
	}
	return out
}

func (m *recordingMarker) marked() []store.MsgKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.keys)
	slices.SortFunc(out, func(a, b store.MsgKey) int {
		return cmp.Or(cmp.Compare(a.Room, b.Room), cmp.Compare(a.Seq, b.Seq))
	})
	return out
}

func closeRig(t *testing.T, rg *rig) {
	t.Helper()
	synctest.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rg.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAckedEventsAreMarked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.enqueue(t, roomA, 1, 2, 3)
		rg.enqueue(t, roomB, 1)
		closeRig(t, rg)
		want := []store.MsgKey{{Room: roomA, Seq: 1}, {Room: roomA, Seq: 2}, {Room: roomA, Seq: 3}, {Room: roomB, Seq: 1}}
		if got := m.marked(); !slices.Equal(got, want) {
			t.Fatalf("marked = %v, want %v", got, want)
		}
	})
}

func TestRefusedEventsAreNotMarked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.js.RefuseWhen(func(*nats.Msg) error { return errRefused })
		rg.enqueue(t, roomA, 1)
		closeRig(t, rg)
		if got := m.marked(); len(got) != 0 {
			t.Fatalf("marked = %v, want none", got)
		}
	})
}

func TestHeldEventsAreMarkedOnlyAfterTheirAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.js.Hold()
		rg.enqueue(t, roomA, 1)
		synctest.Wait()
		if got := m.marked(); len(got) != 0 {
			t.Fatalf("marked before ack = %v, want none", got)
		}
		rg.js.Release()
		closeRig(t, rg)
		if got := m.marked(); !slices.Equal(got, []store.MsgKey{{Room: roomA, Seq: 1}}) {
			t.Fatalf("marked after ack = %v", got)
		}
	})
}

func TestMarkFailureIsOnlyLogged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{err: errors.New("redis down")}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.enqueue(t, roomA, 1)
		closeRig(t, rg)
		if got := rg.sink.Count(markFailedMsg); got != 1 {
			t.Fatalf("%q logged %d times, want 1", markFailedMsg, got)
		}
		if ids := storedIDs(rg.js); len(ids) != 1 {
			t.Fatalf("stored = %v, want the event despite the mark failure", ids)
		}
	})
}

func TestAcksWithinTheWindowShareOneMark(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		for seq := uint64(1); seq <= 10; seq++ {
			rg.enqueue(t, roomA, seq)
			synctest.Wait()
			if seq < 10 {
				time.Sleep(time.Millisecond)
			}
		}
		if got := m.calls(); len(got) != 0 {
			t.Fatalf("Mark calls inside the window = %v, want none", got)
		}
		time.Sleep(markWindow)
		synctest.Wait()
		if got := m.calls(); !slices.Equal(got, []int{10}) {
			t.Fatalf("Mark calls after the window = %v, want [10]", got)
		}
		closeRig(t, rg)
		if got := m.calls(); !slices.Equal(got, []int{10}) {
			t.Fatalf("Mark calls after Close = %v, want [10]", got)
		}
	})
}

func TestFullBatchIsMarkedWithoutWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.enqueue(t, roomA, seqs(1, markBatch+1)...)
		synctest.Wait()
		if got := m.calls(); !slices.Equal(got, []int{markBatch}) {
			t.Fatalf("Mark calls before the window = %v, want [%d]", got, markBatch)
		}
		closeRig(t, rg)
		if got := m.calls(); !slices.Equal(got, []int{markBatch, 1}) {
			t.Fatalf("Mark calls after Close = %v, want [%d 1]", got, markBatch)
		}
	})
}

func TestCloseMarksKeysInAnOpenWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.enqueue(t, roomA, 1, 2, 3)
		synctest.Wait()
		if got := m.calls(); len(got) != 0 {
			t.Fatalf("Mark calls inside the window = %v, want none", got)
		}
		start := time.Now()
		closeRig(t, rg)
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("Close waited %v for the window, want 0", waited)
		}
		if got := m.calls(); !slices.Equal(got, []int{3}) {
			t.Fatalf("Mark calls after Close = %v, want [3]", got)
		}
	})
}
