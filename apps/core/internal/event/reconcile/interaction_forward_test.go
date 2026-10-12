package reconcile_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestForwardsBookmarksAndFlaggedRepliesButNoReplyLinks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		now := time.Now().UTC()
		m := domain.Message{Room: room, Seq: 2, Tenant: tenant, From: "bob", Kind: domain.KindText, Text: "ok", CID: "c2", CreatedAt: now, ReplyTo: &domain.ReplyRef{Seq: 1}, MentionAll: true}
		if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert reply: %+v", res)
		}
		reply := domain.Reply{Parent: domain.MsgKey{Room: room, Seq: 1}, Room: room, Seq: 2, Tenant: tenant, From: "bob", At: now}
		if _, err := rg.reactions.AddReply(t.Context(), reply); err != nil {
			t.Fatalf("AddReply: %v", err)
		}
		b := domain.Bookmark{Room: room, Seq: 1, Tenant: tenant, User: "alice", On: true, At: now}
		if _, _, err := rg.reactions.SetBookmark(t.Context(), b); err != nil {
			t.Fatalf("SetBookmark: %v", err)
		}
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{"m:4242-0-2", "b:4242-bm-0-1-alice-v1"}) {
			t.Fatalf("stored = %v, want the reply message and the bookmark record only", got)
		}
		stored := rg.js.Stored()
		msg, err := work.Decode(stored[0].Data)
		if err != nil || msg.Kind != store.MessageInserted || msg.Version != uint32(store.HasReply|store.HasMention) {
			t.Fatalf("message record = %+v, %v; want reply and mention flags", msg, err)
		}
		mark, err := work.Decode(stored[1].Data)
		if err != nil || mark.Kind != store.BookmarkChanged || mark.Room != room || mark.Seq != 1 || mark.Version != 1 || mark.User != "alice" {
			t.Fatalf("bookmark record = %+v, %v; want %d/0/1 v1 by alice", mark, err, room)
		}
		if got := rg.Stats().Dropped; got != 0 {
			t.Fatalf("dropped = %d, want 0", got)
		}
	})
}
