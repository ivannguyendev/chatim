package sweep_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const foreignMsg = "pending state holds bits its collection does not know; they stay set"

type foreignStates struct {
	store.EventStates
	doc     store.PendingDoc
	mu      sync.Mutex
	fromAll int
}

func (f *foreignStates) Pending(ctx context.Context, c store.StateCollection, from, before time.Time, after store.PendingCursor, limit int) ([]store.PendingDoc, store.PendingCursor, error) {
	docs, next, err := f.EventStates.Pending(ctx, c, from, before, after, limit)
	if err != nil || c != f.doc.Collection || !after.IsZero() {
		return docs, next, err
	}
	f.mu.Lock()
	if from.IsZero() && limit > 1 {
		f.fromAll++
	}
	f.mu.Unlock()
	if f.doc.StateAt.Before(from) || !f.doc.StateAt.Before(before) {
		return docs, next, nil
	}
	return append([]store.PendingDoc{f.doc}, docs...)[:min(limit, len(docs)+1)], next, nil
}

func (f *foreignStates) fullScans() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fromAll
}

func TestForeignBitsWarnOnceAndNeverKeepTheFullScanGoing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		ancient := time.Now().Add(-time.Hour)
		foreign := &foreignStates{EventStates: rg.states, doc: store.PendingDoc{
			Collection: store.StateMessages, Room: room, Seq: 9, State: 8, StateAt: ancient,
		}}
		rg.replaceStates(t, foreign)
		rg.start()
		rg.nextPass()
		rg.nextPass()
		if n := foreign.fullScans(); n != 1 {
			t.Fatalf("messages scanned from zero %d times in 3 passes, want only the first pass", n)
		}
		if n := rg.sink.Count(foreignMsg); n != 1 {
			t.Fatalf("%q logged %d times, want 1", foreignMsg, n)
		}
		if rg.Failures() != 0 || rg.PendingOldest(store.StateMessages) != 0 {
			t.Fatalf("failures %d, oldest %v; want the foreign doc ignored", rg.Failures(), rg.PendingOldest(store.StateMessages))
		}
		rg.stop(t)
	})
}

func TestGoneWithAnUnknownVersionKeepsTheFlag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, func(r sweep.Rebuilder) sweep.Rebuilder {
			return rebuildFunc(func(ctx context.Context, d store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
				if d.Collection == store.StateMessages {
					return nil, domain.ErrMessageNotFound
				}
				return r.Rebuild(ctx, d, bit)
			})
		})
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		rg.send(t, room, 1, old)
		if _, err := rg.msgs.AddReactionCounts(t.Context(), store.MsgKey{Room: room, Seq: 1}, []store.EmojiDelta{{Emoji: "👍", Delta: 1}}); err != nil {
			t.Fatalf("AddReactionCounts: %v", err)
		}
		rg.start()
		docs := rg.pending(t, store.StateMessages)
		if len(docs) != 1 || docs[0].State != store.BitReactionCounts {
			t.Fatalf("pending = %+v, want only the versioned bit kept", docs)
		}
		if rg.Dropped(store.StateMessages) != 1 || rg.Failures() != 1 {
			t.Fatalf("dropped %d, failures %d; want 1 and 1", rg.Dropped(store.StateMessages), rg.Failures())
		}
		expectCheckpoint(t, rg, store.StateMessages, old)
		rg.stop(t)
	})
}

func TestCloseDuringACollectionSkipsTheRemainingCollections(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		closed := make(chan error, 1)
		var rg *rig
		rg = newRig(t, setup, func(r sweep.Rebuilder) sweep.Rebuilder {
			return rebuildFunc(func(ctx context.Context, d store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
				if d.Collection == store.StateMessages {
					go func() { closed <- rg.Close(context.Background()) }()
					time.Sleep(time.Millisecond)
				}
				return r.Rebuild(ctx, d, bit)
			})
		})
		counting := &countingStates{EventStates: rg.states, probes: map[store.StateCollection]int{}, pages: map[store.StateCollection]int{}}
		rg.replaceStates(t, counting)
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		rg.send(t, room, 1, old)
		rg.start()
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if err := <-rg.done; err != nil {
			t.Fatalf("Run = %v", err)
		}
		if probes, pages := counting.counts(store.StateRooms); probes+pages != 0 || rg.Passes() != 0 {
			t.Fatalf("rooms read %d times, passes %d; want the pass to stop after messages", probes+pages, rg.Passes())
		}
		expectPending(t, rg, store.StateRooms, 1)
	})
}
