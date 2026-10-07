package work_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestKnownKindsAreTheTenChangeKinds(t *testing.T) {
	for k := range store.ChangeKind(13) {
		want := k >= store.MessageInserted && k <= store.MemberCountCheck
		if got := work.KnownKind(k); got != want {
			t.Errorf("KnownKind(%d) = %v, want %v", k, got, want)
		}
	}
}

func TestMemberSetRecordsNeedAValidUserTail(t *testing.T) {
	for _, kind := range []store.ChangeKind{store.MemberChanged, store.ReadChanged, store.MessageHidden, store.HistoryCleared} {
		r := work.Record{Kind: kind, Room: 777, Thread: 3, Seq: 5, Version: 2, User: "alice", CommittedAt: committed}
		got, err := work.Decode(work.Encode(r))
		if err != nil || got.Kind != kind || got.User != "alice" || got.Room != 777 || got.Version != 2 {
			t.Fatalf("Decode(Encode(kind %d)) = %+v, %v", kind, got, err)
		}
		for name, user := range map[string]string{"no user": "", "bad user": "a.b"} {
			r.User = user
			b := work.Encode(r)
			if user != "" {
				b = append(b[:work.RecordSize], byte(len(user)))
				b = append(b, user...)
			}
			if _, err := work.Decode(b); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
				t.Errorf("kind %d with %s: Decode = %v, want a malformed record", kind, name, err)
			}
		}
	}
}

func TestMemberCountCheckRecordsCarryNoUser(t *testing.T) {
	r := work.Record{Kind: store.MemberCountCheck, Room: 777, Version: 99, CommittedAt: committed}
	b := work.Encode(r)
	got, err := work.Decode(b)
	if len(b) != work.RecordSize || err != nil || got.Room != 777 || got.Version != 99 || got.User != "" {
		t.Fatalf("Decode(Encode(count check)) = %d bytes, %+v, %v", len(b), got, err)
	}
	if _, err := work.Decode(append(b, 1, 'a')); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
		t.Fatalf("count check with a user: Decode = %v, want a malformed record", err)
	}
}

func TestMemberSetIDsAreNaturalKeys(t *testing.T) {
	cleared := time.UnixMilli(1_700_000_000_123).UTC()
	cases := map[string]struct {
		r    work.Record
		want string
	}{
		"member":       {work.Record{Kind: store.MemberChanged, Room: 777, Version: 1, User: "alice"}, "g:777-mb-alice-v1"},
		"read":         {work.Record{Kind: store.ReadChanged, Room: 777, Version: 3, User: "alice"}, "d:777-rd-alice-v3"},
		"hidden":       {work.Record{Kind: store.MessageHidden, Room: 777, Seq: 5, User: "alice"}, "h:777-hd-alice-0-5"},
		"thread hide":  {work.Record{Kind: store.MessageHidden, Room: 777, Thread: 2, Seq: 5, User: "alice"}, "h:777-hd-alice-2-5"},
		"cleared":      {work.Record{Kind: store.HistoryCleared, Room: 777, User: "alice", CommittedAt: cleared}, "c:777-cl-alice-1700000000123"},
		"count check":  {work.Record{Kind: store.MemberCountCheck, Room: 777, Version: 4_000_000_000}, "k:777-4000000000"},
		"zero version": {work.Record{Kind: store.MemberCountCheck, Room: 777}, "k:777-0"},
	}
	for name, c := range cases {
		if got := c.r.ID(); got != c.want {
			t.Errorf("%s: ID = %q, want %q", name, got, c.want)
		}
	}
}
