package resync_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func interactionMark(room, seq uint64, kind keys.InteractionKind, user string, ver uint32) store.StateTarget {
	return store.StateTarget{Collection: store.StateInteractions, Bit: store.BitInteraction, Room: room, Seq: seq, User: user, Kind: kind, Ver: uint64(ver)}
}

func (w world) bookmark(t *testing.T, room, seq uint64, user string, on bool, at time.Time) store.StateTarget {
	t.Helper()
	b, changed, err := w.reactions.SetBookmark(t.Context(), domain.Bookmark{Room: room, Seq: seq, Tenant: "acme", User: user, On: on, At: at})
	if err != nil || !changed {
		t.Fatalf("SetBookmark(%d/%d %s %v) = %+v, %v, %v; want a change", room, seq, user, on, b, changed, err)
	}
	return interactionMark(room, seq, keys.BookmarkKind, user, b.Ver)
}

func (w world) reply(t *testing.T, room, parent, seq uint64, at time.Time) domain.Reply {
	t.Helper()
	r := domain.Reply{Parent: domain.MsgKey{Room: room, Seq: parent}, Room: room, Seq: seq, Tenant: "acme", From: "bob", At: at}
	if added, err := w.reactions.AddReply(t.Context(), r); err != nil || !added {
		t.Fatalf("AddReply(%d/%d -> %d) = %v, %v; want added", room, seq, parent, added, err)
	}
	return r
}

func TestResyncMarksBookmarksAndChecksCountsOfReactedAndRepliedMessages(t *testing.T) {
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
	reacted := w.react(t, busyRoom, 12, "dave", "👍", lostFrom.Add(5*time.Minute))
	spy := w.spy()
	opts := resync.Options{From: lostFrom, To: lostTo, Room: busyRoom, Rate: resync.MaxRate}
	rep, err := resync.Run(t.Context(), w.deps(spy), opts)
	want := resync.Report{Rooms: 1, MemberCounts: 1, Messages: 61, Reactions: 1, Bookmarks: 2, CountChecks: 3}
	if err != nil || rep != want {
		t.Fatalf("Run = %+v, %v; want %+v", rep, err, want)
	}
	marks := slices.Concat([]store.StateTarget{roomMark(busyRoom, store.BitMemberCount, 1)}, timelineMarks(busyRoom, 91, 31), []store.StateTarget{reacted, off, on})
	assertMarked(t, spy.marked(), marks)
	checks := []string{countCheckID(busyRoom, 12, "reactions"), countCheckID(busyRoom, 12, "replies"), countCheckID(busyRoom, 14, "replies")}
	if !slices.Equal(spy.checks, checks) {
		t.Fatalf("count checks = %v, want %v armed as timers", spy.checks, checks)
	}
}
