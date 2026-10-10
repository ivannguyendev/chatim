package resync_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func countCheckID(room, seq uint64, counter string) string {
	return "q:" + strconv.FormatUint(room, 10) + "-0-" + strconv.FormatUint(seq, 10) + "-" + counter + "-"
}

func (w world) bookmark(t *testing.T, room, seq uint64, user string, on bool, at time.Time) string {
	t.Helper()
	b, changed, err := w.reactions.SetBookmark(t.Context(), domain.Bookmark{Room: room, Seq: seq, Tenant: "acme", User: user, On: on, At: at})
	if err != nil || !changed {
		t.Fatalf("SetBookmark(%d/%d %s %v) = %+v, %v, %v; want a change", room, seq, user, on, b, changed, err)
	}
	return work.Record{Kind: store.BookmarkChanged, Room: room, Seq: seq, Version: b.Ver, User: user}.ID()
}

func (w world) reply(t *testing.T, room, parent, seq uint64, at time.Time) domain.Reply {
	t.Helper()
	r := domain.Reply{Parent: domain.MsgKey{Room: room, Seq: parent}, Room: room, Seq: seq, Tenant: "acme", From: "bob", At: at}
	if added, err := w.reactions.AddReply(t.Context(), r); err != nil || !added {
		t.Fatalf("AddReply(%d/%d -> %d) = %v, %v; want added", room, seq, parent, added, err)
	}
	return r
}

func TestResyncPublishesBookmarksAndChecksCountsOfReactedAndRepliedMessages(t *testing.T) {
	w := newWorld(t)
	w.bookmark(t, busyRoom, 40, "bob", true, lostFrom.Add(-time.Minute))
	off := w.bookmark(t, busyRoom, 40, "bob", false, lostFrom.Add(2*time.Minute))
	on := w.bookmark(t, busyRoom, 41, "carol", true, lostFrom.Add(3*time.Minute))
	w.reply(t, busyRoom, 10, 30, lostFrom.Add(-time.Minute))
	w.reply(t, busyRoom, 12, 32, lostFrom.Add(time.Minute))
	w.reply(t, busyRoom, 12, 33, lostFrom.Add(2*time.Minute))
	gone := w.reply(t, busyRoom, 14, 34, lostFrom.Add(-2*time.Minute))
	if removed, err := w.reactions.RemoveReply(t.Context(), gone, lostFrom.Add(4*time.Minute)); err != nil || !removed {
		t.Fatalf("RemoveReply = %v, %v; want removed", removed, err)
	}
	w.react(t, busyRoom, 12, "dave", "👍", lostFrom.Add(5*time.Minute))
	pub := &publishSpy{}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: busyRoom, Rate: resync.MaxRate}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, opts)
	want := resync.Report{Rooms: 1, MessageRecords: 61, ReactionRecords: 1, BookmarkRecords: 2, CountCheckRecords: 3}
	if err != nil || rep != want {
		t.Fatalf("Run = %+v, %v; want %+v", rep, err, want)
	}
	got := withoutOps(pub.published())
	tail := []string{
		work.Record{Kind: store.ReactionChanged, Room: busyRoom, Seq: 12, Version: 1, User: "dave"}.ID(), off, on,
		countCheckID(busyRoom, 12, "reactions"), countCheckID(busyRoom, 12, "replies"), countCheckID(busyRoom, 14, "replies"),
	}
	if len(got) != 67 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v", len(got), got[max(0, len(got)-6):], tail)
	}
	for _, r := range pub.records()[64:] {
		if r.Kind != store.MessageCountCheck || r.CommittedAt.IsZero() {
			t.Fatalf("count check record = %+v, want a MessageCountCheck stamped with the resync time", r)
		}
	}
}

func TestResyncMessageRecordsCarryTheReplyAndMentionFlagsOfTheirDoc(t *testing.T) {
	w := newWorld(t)
	at := lostFrom.Add(time.Minute)
	msgs := []domain.Message{
		{Room: newRoom, Seq: 1, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "root", CID: "c1", CreatedAt: at},
		{Room: newRoom, Seq: 2, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "re", CID: "c2", CreatedAt: at, ReplyTo: &domain.ReplyRef{Seq: 1}},
		{Room: newRoom, Seq: 3, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "@bob", CID: "c3", CreatedAt: at, Mentions: []domain.MentionTarget{{Kind: domain.MentionUser, ID: "bob"}}},
		{Room: newRoom, Seq: 4, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "@all re", CID: "c4", CreatedAt: at, ReplyTo: &domain.ReplyRef{Seq: 1}, MentionAll: true},
	}
	for i, r := range w.msgs.Insert(t.Context(), msgs) {
		if r.Outcome != store.Inserted {
			t.Fatalf("insert %d: %+v", i+1, r)
		}
	}
	pub := &publishSpy{}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: newRoom, Rate: resync.MaxRate}
	if _, err := resync.Run(t.Context(), w.deps(pub), target, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	flags := map[uint64]store.ReplyMentionFlags{}
	for _, r := range pub.records() {
		if r.Kind == store.MessageInserted {
			flags[r.Seq] = store.ReplyMentionFlags(r.Version)
		}
	}
	for _, m := range msgs {
		if got, want := flags[m.Seq], store.ReplyMentionFlagsOf(m); got != want {
			t.Errorf("seq %d flags = %d, want %d like the feed record", m.Seq, got, want)
		}
	}
}
