package storetest

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func bookmarkAt(room, thread, seq uint64, user string, on bool, after time.Duration) domain.Bookmark {
	return domain.Bookmark{Room: room, Thread: thread, Seq: seq, Tenant: tenant, User: user, On: on, At: baseTime.Add(after)}
}

func replyAt(room, parentSeq, seq uint64, from string, after time.Duration) domain.Reply {
	return domain.Reply{
		Parent: domain.MsgKey{Room: room, Thread: mainThread, Seq: parentSeq}, Room: room, Thread: mainThread, Seq: seq,
		Tenant: tenant, From: from, At: baseTime.Add(after),
	}
}

func live(r domain.Reply) domain.Reply {
	r.Live, r.Ver = true, 1
	return r
}

func versioned(b domain.Bookmark, ver uint32) domain.Bookmark {
	b.Ver = ver
	return b
}

func mustSetBookmark(t *testing.T, s store.Interactions, b domain.Bookmark, wantChanged bool) domain.Bookmark {
	t.Helper()
	got, ok, err := s.SetBookmark(t.Context(), b)
	if err != nil || ok != wantChanged {
		t.Fatalf("SetBookmark(%v by %q on %d/%d/%d) = %+v, %v, %v; want changed %v", b.On, b.User, b.Room, b.Thread, b.Seq, got, ok, err, wantChanged)
	}
	return got
}

func mustAddReply(t *testing.T, s store.Interactions, r domain.Reply, wantInserted bool) {
	t.Helper()
	if ok, err := s.AddReply(t.Context(), r); err != nil || ok != wantInserted {
		t.Fatalf("AddReply(%d to %d) = %v, %v; want inserted %v", r.Seq, r.Parent.Seq, ok, err, wantInserted)
	}
}

func mustRemoveReply(t *testing.T, s store.Interactions, parent, reply store.MsgKey, wantRemoved bool) {
	t.Helper()
	if ok, err := s.RemoveReply(t.Context(), parent, reply, baseTime.Add(time.Hour)); err != nil || ok != wantRemoved {
		t.Fatalf("RemoveReply(%d from %d) = %v, %v; want removed %v", reply.Seq, parent.Seq, ok, err, wantRemoved)
	}
}

func reactionRefs(rs ...domain.Reaction) []store.Interaction {
	out := make([]store.Interaction, len(rs))
	for i, r := range rs {
		out[i] = store.Interaction{Kind: keys.ReactionKind, Key: store.ReactionKeyOf(r), User: r.User, Ver: r.N, At: r.At}
	}
	return out
}

func sameAt[T any](at func(T) time.Time, clear func(T) T) func(a, b T) bool {
	return func(a, b T) bool {
		return at(a).Equal(at(b)) && reflect.DeepEqual(clear(a), clear(b))
	}
}

func assertInteractions(t *testing.T, op string, got, want []store.Interaction) {
	t.Helper()
	same := sameAt(func(x store.Interaction) time.Time { return x.At }, func(x store.Interaction) store.Interaction { x.At = time.Time{}; return x })
	if !slices.EqualFunc(got, want, same) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func assertReplies(t *testing.T, s store.Interactions, parent store.MsgKey, after uint64, limit int, want ...domain.Reply) {
	t.Helper()
	got, err := s.Replies(t.Context(), parent, after, limit)
	same := sameAt(func(r domain.Reply) time.Time { return r.At }, func(r domain.Reply) domain.Reply { r.At = time.Time{}; return r })
	if err != nil || !slices.EqualFunc(got, want, same) {
		t.Fatalf("Replies(%d after %d, limit %d) = %+v, %v;\nwant %+v", parent.Seq, after, limit, got, err, want)
	}
}

func assertLiveReplies(t *testing.T, s store.Interactions, parent store.MsgKey, want uint32) {
	t.Helper()
	if got, err := s.CountLiveReplies(t.Context(), parent); err != nil || got != want {
		t.Fatalf("CountLiveReplies(%d) = %d, %v; want %d", parent.Seq, got, err, want)
	}
}

func assertBookmarks(t *testing.T, op string, got []domain.Bookmark, err error, want ...domain.Bookmark) {
	t.Helper()
	same := sameAt(func(b domain.Bookmark) time.Time { return b.At }, func(b domain.Bookmark) domain.Bookmark { b.At = time.Time{}; return b })
	if err != nil || !slices.EqualFunc(got, want, same) {
		t.Fatalf("%s = %+v, %v;\nwant %+v", op, got, err, want)
	}
}

func assertStoredBookmark(t *testing.T, s store.Interactions, want domain.Bookmark) {
	t.Helper()
	got, ok, err := s.GetBookmark(t.Context(), store.BookmarkKeyOf(want), want.User)
	if !ok {
		t.Fatalf("GetBookmark(%q on %+v) = %+v, %v, %v; want %+v", want.User, store.BookmarkKeyOf(want), got, ok, err, want)
	}
	assertBookmarks(t, "GetBookmark", []domain.Bookmark{got}, err, want)
}
