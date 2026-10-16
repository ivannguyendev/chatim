package sweep_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestHiddenMessageIsPublishedAtItsHideTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
		f.group(t, old, "ann")
		f.send(t, room, 1, old)
		hiddenAt := old.Add(time.Second)
		if first, err := f.hidden.Hide(t.Context(), "ann", store.MsgKey{Room: room, Seq: 1}, hiddenAt); err != nil || !first {
			t.Fatalf("Hide = %v, %v", first, err)
		}
		f.settle()
		ev := f.published(t, pbconv.HiddenEventID(room, "ann", 0, 1))
		if ev == nil || !ev.GetTs().AsTime().Equal(hiddenAt) {
			t.Fatalf("message_hidden = %+v, want ts %v", ev, hiddenAt)
		}
		expectPending(t, f.rig, store.StateHidden, 0)
		f.stop(t)
	})
}

type vanishedInteractions struct{ *memstore.Interactions }

func (vanishedInteractions) GetReaction(context.Context, store.MsgKey, string) (domain.Reaction, bool, error) {
	return domain.Reaction{}, false, nil
}

func (vanishedInteractions) GetBookmark(context.Context, store.MsgKey, string) (domain.Bookmark, bool, error) {
	return domain.Bookmark{}, false, nil
}

type vanishedHidden struct{ *memstore.Hidden }

func (vanishedHidden) Get(context.Context, string, store.MsgKey) (domain.HiddenMessage, bool, error) {
	return domain.HiddenMessage{}, false, nil
}

func TestVanishedInteractionAndHideAreDroppedAndCleared(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, func(f *fullRig) sweep.RebuilderStores {
			s := f.stores(t)
			s.Interactions = vanishedInteractions{f.inter}
			s.Hidden = vanishedHidden{f.hidden}
			return s
		})
		rec := &recordingStates{EventStates: f.states}
		f.replaceStates(t, rec)
		at := time.Now().Add(-time.Minute)
		f.group(t, at, "ann")
		seeds := []store.StateTarget{
			{Collection: store.StateInteractions, Bit: store.BitInteraction, Room: room, Seq: 1, User: "ann", Kind: keys.ReactionKind, Ver: 1},
			{Collection: store.StateInteractions, Bit: store.BitInteraction, Room: room, Seq: 1, User: "ann", Kind: keys.BookmarkKind, Ver: 1},
			{Collection: store.StateHidden, Bit: store.BitHidden, Room: room, Seq: 1, User: "ann"},
		}
		for _, s := range seeds {
			if err := f.states.SeedState(t.Context(), s, at); err != nil {
				t.Fatalf("SeedState: %v", err)
			}
		}
		f.settle()
		expectPending(t, f.rig, store.StateHidden, 0)
		if n := len(clearedIn(rec, store.StateInteractions)); n != 2 {
			t.Fatalf("interaction clears = %d, want 2", n)
		}
		if f.Dropped(store.StateInteractions) != 2 || f.Dropped(store.StateHidden) != 1 {
			t.Fatalf("dropped interactions %d, hidden %d; want 2 and 1", f.Dropped(store.StateInteractions), f.Dropped(store.StateHidden))
		}
		f.stop(t)
	})
}

func TestNewRebuildersNeedsEveryStore(t *testing.T) {
	if _, err := sweep.NewRebuilders(sweep.RebuilderStores{}, nil, nil); err == nil {
		t.Fatal("NewRebuilders without stores succeeded")
	}
}
