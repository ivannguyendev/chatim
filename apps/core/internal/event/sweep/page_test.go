package sweep_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type recordingStates struct {
	store.EventStates
	mu      sync.Mutex
	cleared []store.StateTarget
}

func (r *recordingStates) Clear(ctx context.Context, targets []store.StateTarget) error {
	r.mu.Lock()
	r.cleared = append(r.cleared, targets...)
	r.mu.Unlock()
	return r.EventStates.Clear(ctx, targets)
}

func (r *recordingStates) clears() []store.StateTarget {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.cleared)
}

func TestEveryBitOfADocBecomesItsOwnEventClearedAtItsVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		rec := &recordingStates{EventStates: rg.states}
		rg.replaceStates(t, rec)
		old := time.Now().Add(-time.Minute)
		r := domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "ann", CreatedAt: old, MemberCount: 1}
		if err := rg.rooms.Create(t.Context(), r, []domain.Member{{Room: room, Tenant: tenant, User: "ann", Role: domain.RoleOwner, JoinedAt: old}}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		rg.send(t, room, 1, old)
		key := store.MsgKey{Room: room, Seq: 1}
		if _, err := rg.msgs.AddReactionCounts(t.Context(), key, []store.EmojiDelta{{Emoji: "👍", Delta: 1}}); err != nil {
			t.Fatalf("AddReactionCounts: %v", err)
		}
		if _, err := rg.msgs.AddReplyCount(t.Context(), key, 1); err != nil {
			t.Fatalf("AddReplyCount: %v", err)
		}
		time.Sleep(minAge + time.Second)
		rg.start()
		expectIDs(t, rg,
			pbconv.RoomCreatedEventID(room), pbconv.MemberCountEventID(room, 1),
			pbconv.MessageEventID(room, 0, 1), pbconv.ReactionCountsEventID(room, 0, 1, 1),
			pbconv.MessageCountsEventID(room, 0, 1, pbconv.RepliesCounter, 1))
		expectPending(t, rg, store.StateMessages, 0)
		expectPending(t, rg, store.StateRooms, 0)
		want := []store.StateTarget{
			{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: room, Seq: 1},
			{Collection: store.StateMessages, Bit: store.BitReactionCounts, Room: room, Seq: 1, Ver: 1},
			{Collection: store.StateMessages, Bit: store.BitReplyCounts, Room: room, Seq: 1, Ver: 1},
			{Collection: store.StateRooms, Bit: store.BitRoomCreated, Room: room},
			{Collection: store.StateRooms, Bit: store.BitMemberCount, Room: room, Ver: 1},
		}
		if got := rec.clears(); !slices.Equal(got, want) {
			t.Fatalf("cleared = %+v, want %+v", got, want)
		}
		rg.stop(t)
	})
}

func TestAVersionMovedDuringTheSweepKeepsItsFlag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var rg *rig
		rg = newRig(t, setup, func(r sweep.Rebuilder) sweep.Rebuilder {
			return rebuildFunc(func(ctx context.Context, d store.PendingDoc, bit store.StateBit) ([]*chatimv1.Event, error) {
				evs, err := r.Rebuild(ctx, d, bit)
				if bit == store.BitReactionCounts {
					_, _ = rg.msgs.AddReactionCounts(ctx, store.MsgKey{Room: d.Room, Seq: d.Seq}, []store.EmojiDelta{{Emoji: "👍", Delta: 1}})
				}
				return evs, err
			})
		})
		old := time.Now().Add(-time.Minute)
		rg.createRoom(t, room, old)
		rg.send(t, room, 1, old)
		if _, err := rg.msgs.AddReactionCounts(t.Context(), store.MsgKey{Room: room, Seq: 1}, []store.EmojiDelta{{Emoji: "👍", Delta: 1}}); err != nil {
			t.Fatalf("AddReactionCounts: %v", err)
		}
		time.Sleep(minAge + time.Second)
		rg.start()
		docs := rg.pending(t, store.StateMessages)
		if len(docs) != 1 || docs[0].State != store.BitReactionCounts {
			t.Fatalf("pending = %+v, want the reaction count bit only", docs)
		}
		rg.stop(t)
	})
}

func TestGoneRoomDropsAndClearsEveryBit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		const lost uint64 = 999
		old := time.Now().Add(-time.Minute)
		rg.send(t, lost, 1, old)
		if _, err := rg.msgs.AddReactionCounts(t.Context(), store.MsgKey{Room: lost, Seq: 1}, []store.EmojiDelta{{Emoji: "👍", Delta: 1}}); err != nil {
			t.Fatalf("AddReactionCounts: %v", err)
		}
		time.Sleep(minAge + time.Second)
		rg.start()
		if n := len(rg.js.Attempts()); n != 0 {
			t.Fatalf("published %d events for a gone room, want 0", n)
		}
		expectPending(t, rg, store.StateMessages, 0)
		expectCheckpoint(t, rg, store.StateMessages, time.Now().Add(-minAge))
		if rg.Dropped(store.StateMessages) != 2 || rg.Failures() != 0 {
			t.Fatalf("dropped %d, failures %d; want 2 and 0", rg.Dropped(store.StateMessages), rg.Failures())
		}
		rg.stop(t)
	})
}

func TestUnpublishableRoomIsDroppedAndCleared(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, setup, nil)
		if err := rg.states.SeedState(t.Context(), store.StateTarget{Collection: store.StateRooms, Bit: store.BitRoomCreated, Room: room}, time.Now().Add(-time.Minute)); err != nil {
			t.Fatalf("SeedState: %v", err)
		}
		rg.start()
		expectPending(t, rg, store.StateRooms, 0)
		if rg.Dropped(store.StateRooms) != 1 || len(rg.js.Attempts()) != 0 {
			t.Fatalf("dropped %d, attempts %d; want 1 and 0", rg.Dropped(store.StateRooms), len(rg.js.Attempts()))
		}
		rg.stop(t)
	})
}
