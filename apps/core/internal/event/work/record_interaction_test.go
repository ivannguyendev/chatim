package work_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestRecordOfMessageKeepsTheReplyMentionFlagsInTheVersion(t *testing.T) {
	c := store.Change{Kind: store.MessageInserted, Msg: domain.Message{Room: 42, Thread: 3, Seq: 9}, ReplyMentionFlags: store.HasReply | store.HasMention, CommittedAt: committed}
	r := work.RecordOf(c)
	if want := (work.Record{Kind: store.MessageInserted, Room: 42, Thread: 3, Seq: 9, Version: 3, CommittedAt: committed}); r != want {
		t.Fatalf("RecordOf(flagged message) = %+v, want %+v", r, want)
	}
	got, err := work.Decode(work.Encode(r))
	if err != nil || got != r || r.ID() != "m:42-3-9" {
		t.Fatalf("Decode(Encode(flagged message)) = %+v, %v, id %q; want the same record and id m:42-3-9", got, err, r.ID())
	}
}

func TestRecordOfBookmarkCarriesTheUserAndVersion(t *testing.T) {
	b := domain.Bookmark{Room: 42, Thread: 3, Seq: 9, Tenant: "acme", User: "bob", On: true, Ver: 2, At: committed}
	r := work.RecordOf(store.Change{Kind: store.BookmarkChanged, Bookmark: b, CommittedAt: committed})
	if want := (work.Record{Kind: store.BookmarkChanged, Room: 42, Thread: 3, Seq: 9, Version: 2, User: "bob", CommittedAt: committed}); r != want {
		t.Fatalf("RecordOf(bookmark) = %+v, want %+v", r, want)
	}
	got, err := work.Decode(work.Encode(r))
	if err != nil || got != r {
		t.Fatalf("Decode(Encode(bookmark)) = %+v, %v; want %+v", got, err, r)
	}
}

func TestInteractionAndCountCheckRecordsNeedAValidTail(t *testing.T) {
	for _, kind := range []store.ChangeKind{store.BookmarkChanged, store.MessageCountCheck} {
		r := work.Record{Kind: kind, Room: 42, Thread: 3, Seq: 9, Version: 7, User: "replies", CommittedAt: committed}
		if got, err := work.Decode(work.Encode(r)); err != nil || got != r {
			t.Fatalf("Decode(Encode(kind %d)) = %+v, %v; want %+v", kind, got, err, r)
		}
		bare := work.Encode(work.Record{Kind: kind, Room: 42, Seq: 9, CommittedAt: committed})
		bad := append(slices.Clone(bare), 3, 'a', '.', 'b')
		for name, b := range map[string][]byte{"no tail": bare, "bad tail": bad} {
			if _, err := work.Decode(b); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
				t.Errorf("kind %d with %s: Decode = %v, want a malformed record", kind, name, err)
			}
		}
	}
}

func TestInteractionAndCountCheckIDsAreNaturalKeys(t *testing.T) {
	cases := map[string]struct {
		r    work.Record
		want string
	}{
		"bookmark":          {work.Record{Kind: store.BookmarkChanged, Room: 42, Seq: 7, Version: 1, User: "bob"}, "b:42-bm-0-7-bob-v1"},
		"thread bookmark":   {work.Record{Kind: store.BookmarkChanged, Room: 42, Thread: 3, Seq: 9, Version: 4, User: "a-v1"}, "b:42-bm-3-9-a-v1-v4"},
		"reaction count":    {work.Record{Kind: store.MessageCountCheck, Room: 42, Seq: 7, Version: 4_000_000_000, User: "reactions"}, "q:42-0-7-reactions-4000000000"},
		"reply count":       {work.Record{Kind: store.MessageCountCheck, Room: 42, Thread: 3, Seq: 9, Version: 5, User: "replies"}, "q:42-3-9-replies-5"},
		"zero op count":     {work.Record{Kind: store.MessageCountCheck, Room: 42, Seq: 7, User: "replies"}, "q:42-0-7-replies-0"},
		"member count kept": {work.Record{Kind: store.MemberCountCheck, Room: 42, Version: 5}, "k:42-5"},
	}
	for name, c := range cases {
		if got := c.r.ID(); got != c.want {
			t.Errorf("%s: ID = %q, want %q", name, got, c.want)
		}
	}
}

func TestCountCheckRecordsCarryOnlyAKnownCounter(t *testing.T) {
	for _, counter := range []string{"bob", "members", "Replies", "reaction"} {
		r := work.Record{Kind: store.MessageCountCheck, Room: 42, Seq: 7, Version: 5, User: counter, CommittedAt: committed}
		if _, err := work.Decode(work.Encode(r)); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
			t.Errorf("Decode(count check of %q) = %v, want a malformed record", counter, err)
		}
		if id := r.ID(); id != "" {
			t.Errorf("ID(count check of %q) = %q, want none", counter, id)
		}
	}
	bookmark := work.Record{Kind: store.BookmarkChanged, Room: 42, Seq: 7, Version: 1, User: "bob", CommittedAt: committed}
	if got, err := work.Decode(work.Encode(bookmark)); err != nil || got != bookmark {
		t.Fatalf("Decode(bookmark by bob) = %+v, %v; want the user tail kept", got, err)
	}
}
