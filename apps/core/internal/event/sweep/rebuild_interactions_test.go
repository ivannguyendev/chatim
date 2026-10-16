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

func (f *fullRig) react(t *testing.T, ctx context.Context, emoji string) {
	t.Helper()
	r := domain.Reaction{Room: room, Seq: 1, Tenant: tenant, User: "bob", Emoji: emoji, At: time.Now().Add(-time.Minute)}
	if _, changed, err := f.inter.SetReaction(ctx, r); err != nil || !changed {
		t.Fatalf("SetReaction(%s) = %v, %v", emoji, changed, err)
	}
}

type racingInteractions struct {
	*memstore.Interactions
	after func()
}

func (r racingInteractions) GetReaction(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error) {
	doc, found, err := r.Interactions.GetReaction(ctx, key, user)
	r.after()
	return doc, found, err
}

func TestReactionChangedDuringTheSweepKeepsItsNewerFlag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		raced := false
		f := newFullRig(t, func(f *fullRig) sweep.RebuilderStores {
			s := f.stores(t)
			s.Interactions = racingInteractions{Interactions: f.inter, after: func() {
				if !raced {
					raced = true
					f.react(t, context.Background(), "❤️")
				}
			}}
			return s
		})
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann", "bob")
		f.send(t, room, 1, old)
		f.react(t, t.Context(), "👍")
		f.settle()
		expectEvents(t, f, pbconv.ReactionEventID(room, 0, 1, "bob", 1))
		docs := f.pending(t, store.StateInteractions)
		if len(docs) != 1 || docs[0].User != "bob" || docs[0].Kind != keys.ReactionKind {
			t.Fatalf("pending interactions = %+v, want the newer reaction still flagged", docs)
		}
		time.Sleep(minAge)
		f.nextPass()
		expectEvents(t, f, pbconv.ReactionEventID(room, 0, 1, "bob", 2))
		expectPending(t, f.rig, store.StateInteractions, 0)
		f.stop(t)
	})
}

func TestBookmarkIsPublishedAtItsCurrentVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFullRig(t, nil)
		old := time.Now().Add(-time.Minute)
		f.group(t, old, "ann")
		f.send(t, room, 1, old)
		for _, on := range []bool{true, false} {
			b := domain.Bookmark{Room: room, Seq: 1, Tenant: tenant, User: "ann", On: on, At: old}
			if _, changed, err := f.inter.SetBookmark(t.Context(), b); err != nil || !changed {
				t.Fatalf("SetBookmark(%v) = %v, %v", on, changed, err)
			}
		}
		f.settle()
		expectEvents(t, f, pbconv.BookmarkEventID(room, 0, 1, "ann", 2))
		if f.event(t, pbconv.BookmarkEventID(room, 0, 1, "ann", 1)) {
			t.Fatal("bookmark_changed published for a version that is no longer current")
		}
		if ev := f.published(t, pbconv.BookmarkEventID(room, 0, 1, "ann", 2)); ev.GetBookmarkChanged().GetOn() {
			t.Fatalf("bookmark_changed = %+v, want off", ev)
		}
		expectPending(t, f.rig, store.StateInteractions, 0)
		f.stop(t)
	})
}
