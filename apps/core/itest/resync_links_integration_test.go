package itest

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func missedLinkMessages(t *testing.T, st *mongostore.Store, room uint64) []string {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Millisecond)
	linked := func(seq uint64, m domain.Message) domain.Message {
		m.Room, m.Seq, m.Tenant, m.From, m.Kind, m.CreatedAt = room, seq, itTenant, "migrator", domain.KindText, at
		m.CID = "missed-" + strconv.FormatUint(seq, 10)
		return m
	}
	msgs := []domain.Message{
		linked(4, domain.Message{Text: "reply to seq 1", ReplyTo: &domain.ReplyRef{Seq: 1}}),
		linked(5, domain.Message{Text: "reply to seq 2", ReplyTo: &domain.ReplyRef{Seq: 2}}),
		linked(6, domain.Message{Text: "@bob", Mentions: []domain.MentionTarget{{Kind: domain.MentionUser, ID: "bob"}}}),
	}
	for i, res := range st.Insert(t.Context(), msgs) {
		if res.Outcome != store.Inserted {
			t.Fatalf("insert linked message %d: %+v", msgs[i].Seq, res)
		}
	}
	reply := domain.Reply{Parent: domain.MsgKey{Room: room, Seq: 2}, Room: room, Seq: 5, Tenant: itTenant, From: "migrator", At: at}
	if added, err := itReactions(st).AddReply(t.Context(), reply); err != nil || !added {
		t.Fatalf("write the reply doc of seq 5 without its count: %v, %v", added, err)
	}
	bookmark := domain.Bookmark{Room: room, Seq: 3, Tenant: itTenant, User: "migrator", On: true, At: at}
	if _, _, err := itReactions(st).SetBookmark(t.Context(), bookmark); err != nil {
		t.Fatalf("set a bookmark the reader missed: %v", err)
	}
	return []string{
		pbconv.MessageEventID(room, 0, 4), pbconv.MessageEventID(room, 0, 5), pbconv.MessageEventID(room, 0, 6),
		pbconv.BookmarkEventID(room, 0, 3, "migrator", 1),
		pbconv.MessageCountsEventID(room, 0, 1, pbconv.RepliesCounter, 1), pbconv.MessageCountsEventID(room, 0, 2, pbconv.RepliesCounter, 1),
	}
}

func assertLinksRebuilt(t *testing.T, st *mongostore.Store, room uint64) {
	t.Helper()
	got, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: 1}, {Room: room, Seq: 2}})
	if err != nil || len(got) != 2 || got[0].Replies.N != 1 || got[1].Replies.N != 1 {
		t.Fatalf("parents after resync = %+v, %v; want one reply counted on seq 1 (rebuilt from the flag) and seq 2 (recounted)", got, err)
	}
	for parent, seq := range map[uint64]uint64{1: 4, 2: 5} {
		replies, err := itReactions(st).Replies(t.Context(), store.MsgKey{Room: room, Seq: parent}, 0, 10)
		if err != nil || len(replies) != 1 || replies[0].Seq != seq {
			t.Fatalf("replies of seq %d = %+v, %v; want seq %d", parent, replies, err, seq)
		}
	}
	mentions, err := st.Mentions().MentionsOf(t.Context(), store.MsgKey{Room: room, Seq: 6})
	targets := make([]domain.MentionTarget, len(mentions))
	for i, m := range mentions {
		targets[i] = m.Target
	}
	if err != nil || !slices.Equal(targets, []domain.MentionTarget{{Kind: domain.MentionUser, ID: "bob"}}) {
		t.Fatalf("mentions of seq 6 = %+v, %v; want bob rebuilt from the flag", mentions, err)
	}
	counted, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: 2}})
	if err != nil || len(counted) != 1 || counted[0].Reactions.Version != 1 ||
		!slices.Equal(counted[0].Reactions.Counts, []domain.ReactionCount{{Emoji: "👍", Count: 1}}) {
		t.Fatalf("seq 2 after resync = %+v, %v; want the uncounted reaction recounted at version 1", counted, err)
	}
}
