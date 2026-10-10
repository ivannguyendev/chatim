package work_test

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

var committed = time.Unix(1_700_000_000, 123_456_789).UTC()

func TestRecordOfKeepsOnlyKeysAndCommitTime(t *testing.T) {
	msg := store.Change{Kind: store.MessageInserted, Msg: domain.Message{Room: 42, Thread: 3, Seq: 9, Text: "hi"}, CommittedAt: committed, Position: store.Position("p")}
	if got, want := work.RecordOf(msg), (work.Record{Kind: store.MessageInserted, Room: 42, Thread: 3, Seq: 9, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(message) = %+v, want %+v", got, want)
	}
	room := store.Change{Kind: store.RoomInserted, Room: domain.Room{ID: 77, Tenant: "acme"}, CommittedAt: committed}
	if got, want := work.RecordOf(room), (work.Record{Kind: store.RoomInserted, Room: 77, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(room) = %+v, want %+v", got, want)
	}
	edit := store.Change{Kind: store.EditInserted, Edit: domain.Edit{Room: 42, Thread: 3, Seq: 9, Version: 2, Text: "x"}, CommittedAt: committed}
	if got, want := work.RecordOf(edit), (work.Record{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, Version: 2, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(edit) = %+v, want %+v", got, want)
	}
	reaction := store.Change{Kind: store.ReactionChanged, Reaction: domain.Reaction{Room: 42, Thread: 3, Seq: 9, User: "bob", N: 4, Emoji: "👍"}, CommittedAt: committed}
	if got, want := work.RecordOf(reaction), (work.Record{Kind: store.ReactionChanged, Room: 42, Thread: 3, Seq: 9, Version: 4, User: "bob", CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(reaction) = %+v, want %+v", got, want)
	}
	pin := store.Change{Kind: store.PinInserted, Pin: domain.PinAction{Room: 42, PV: 6, Op: domain.PinOpPin, Thread: 3, Seq: 9, By: "bob"}, CommittedAt: committed}
	if got, want := work.RecordOf(pin), (work.Record{Kind: store.PinInserted, Room: 42, Seq: 6, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(pin) = %+v, want %+v", got, want)
	}
}

func TestRecordOfMemberSetChanges(t *testing.T) {
	m := domain.Member{Room: 42, User: "bob", Role: domain.RoleAdmin, State: domain.MemberActive, Ver: 5, ReadSeq: 30, ReadVer: 8}
	h := domain.HiddenMessage{User: "bob", Room: 42, Thread: 3, Seq: 9, At: committed}
	cases := map[store.ChangeKind]struct {
		c    store.Change
		want work.Record
	}{
		store.MemberChanged:    {store.Change{Member: m}, work.Record{Room: 42, Version: 5, User: "bob"}},
		store.ReadChanged:      {store.Change{Member: domain.Member{Room: 42, User: "bob", ReadVer: 8}}, work.Record{Room: 42, Version: 8, User: "bob"}},
		store.MessageHidden:    {store.Change{Hidden: h}, work.Record{Room: 42, Thread: 3, Seq: 9, User: "bob"}},
		store.HistoryCleared:   {store.Change{Member: domain.Member{Room: 42, User: "bob"}}, work.Record{Room: 42, User: "bob"}},
		store.MemberCountCheck: {store.Change{Room: domain.Room{ID: 42}}, work.Record{Room: 42}},
	}
	for kind, tc := range cases {
		tc.c.Kind, tc.c.CommittedAt = kind, committed
		tc.want.Kind, tc.want.CommittedAt = kind, committed
		if got := work.RecordOf(tc.c); got != tc.want {
			t.Errorf("RecordOf(kind %d) = %+v, want %+v", kind, got, tc.want)
		}
	}
	huge := store.Change{Kind: store.ReadChanged, Member: domain.Member{Room: 42, User: "bob", ReadVer: math.MaxUint32 + 1}}
	if got := work.RecordOf(huge); got.Version != math.MaxUint32 {
		t.Fatalf("RecordOf(read ver past uint32) version = %d, want it held at MaxUint32", got.Version)
	}
}

func TestRecordRoundTripsThroughThirtySevenBytes(t *testing.T) {
	for _, r := range []work.Record{
		{Kind: store.MessageInserted, Room: 42, Thread: 3, Seq: 9, CommittedAt: committed},
		{Kind: store.RoomInserted, Room: math.MaxInt64, CommittedAt: committed},
		{Kind: store.MessageInserted, Room: 1, Seq: math.MaxUint64, CommittedAt: committed},
		{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, Version: math.MaxUint32, CommittedAt: committed},
	} {
		b := work.Encode(r)
		if len(b) != work.RecordSize || work.RecordSize != 37 {
			t.Fatalf("Encode(%+v) is %d bytes, RecordSize %d; want 37", r, len(b), work.RecordSize)
		}
		got, err := work.Decode(b)
		if err != nil || got.Kind != r.Kind || got.Room != r.Room || got.Thread != r.Thread || got.Seq != r.Seq || got.Version != r.Version || !got.CommittedAt.Equal(r.CommittedAt) {
			t.Fatalf("Decode(Encode(%+v)) = %+v, %v", r, got, err)
		}
	}
}

func TestEncodeIsBigEndianAndClampsTimesBeforeTheEpoch(t *testing.T) {
	want := make([]byte, work.RecordSize)
	want[0], want[8], want[16], want[24], want[28], want[36] = 3, 1, 2, 3, 5, 4
	if got := work.Encode(work.Record{Kind: store.EditInserted, Room: 1, Thread: 2, Seq: 3, Version: 5, CommittedAt: time.Unix(0, 4)}); !bytes.Equal(got, want) {
		t.Fatalf("Encode = %x, want %x", got, want)
	}
	got, err := work.Decode(work.Encode(work.Record{Kind: store.RoomInserted, Room: 5}))
	if err != nil || !got.CommittedAt.Equal(time.Unix(0, 0)) {
		t.Fatalf("zero commit time decodes to %v, %v; want the epoch", got.CommittedAt, err)
	}
}

func TestDecodeRejectsBadRecords(t *testing.T) {
	good := work.Encode(work.Record{Kind: store.RoomInserted, Room: 7, CommittedAt: committed})
	zeroKind, unknownKind, pastInt64 := slices.Clone(good), slices.Clone(good), slices.Clone(good)
	zeroKind[0], unknownKind[0], pastInt64[29] = 0, 13, 0x80
	for name, b := range map[string][]byte{
		"empty":           nil,
		"short":           good[:work.RecordSize-1],
		"long":            append(slices.Clone(good), 0),
		"zero kind":       zeroKind,
		"unknown kind":    unknownKind,
		"time past int64": pastInt64,
	} {
		if _, err := work.Decode(b); !errors.Is(err, work.ErrBadRecord) || !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: Decode = %v, want ErrBadRecord", name, err)
		}
	}
}

func TestIDsAreNaturalKeys(t *testing.T) {
	cases := map[string]struct {
		r    work.Record
		want string
	}{
		"message":         {work.Record{Kind: store.MessageInserted, Room: 42, Seq: 7, CommittedAt: committed}, "m:42-0-7"},
		"thread message":  {work.Record{Kind: store.MessageInserted, Room: 42, Thread: 3, Seq: 9}, "m:42-3-9"},
		"room":            {work.Record{Kind: store.RoomInserted, Room: 42, CommittedAt: committed}, "r:42"},
		"edit":            {work.Record{Kind: store.EditInserted, Room: 42, Seq: 7, Version: 1}, "e:42-0-7-v1"},
		"thread edit":     {work.Record{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, Version: 12}, "e:42-3-9-v12"},
		"reaction":        {work.Record{Kind: store.ReactionChanged, Room: 42, Seq: 7, Version: 3, User: "bob"}, "x:42-0-7-bob-n3"},
		"thread reaction": {work.Record{Kind: store.ReactionChanged, Room: 42, Thread: 3, Seq: 9, Version: 1, User: "a-n1"}, "x:42-3-9-a-n1-n1"},
		"pin":             {work.Record{Kind: store.PinInserted, Room: 42, Seq: 5}, "p:42-p5"},
		"unknown kind":    {work.Record{Room: 42}, ""},
	}
	for name, c := range cases {
		if got := c.r.ID(); got != c.want {
			t.Errorf("%s: ID = %q, want %q", name, got, c.want)
		}
	}
	later := work.Record{Kind: store.MessageInserted, Room: 42, Seq: 7, CommittedAt: committed.Add(time.Hour)}
	if later.ID() != "m:42-0-7" {
		t.Errorf("ID depends on the commit time: %q", later.ID())
	}
}

func TestPartitionFollowsTheRoomSlot(t *testing.T) {
	seen := map[int]bool{}
	for i := range uint64(4096) {
		room := i + 1
		p := work.Partition(room, 32)
		if p != int(slotmap.Of(room))%32 || p != work.Partition(room, 32) {
			t.Fatalf("Partition(%d, 32) = %d, want slot %d %% 32 every time", room, p, slotmap.Of(room))
		}
		seen[p] = true
	}
	if len(seen) != 32 {
		t.Fatalf("4096 rooms fell into %d partitions, want all 32", len(seen))
	}
	if p := work.Partition(42, 1); p != 0 {
		t.Fatalf("Partition(42, 1) = %d, want 0", p)
	}
}

func TestSubjectNamesThePartition(t *testing.T) {
	for p, want := range map[int]string{0: "work.p0", 7: "work.p7", 31: "work.p31"} {
		if got := work.Subject("work", p); got != want {
			t.Errorf("Subject(work, %d) = %q, want %q", p, got, want)
		}
	}
}

func TestMessageCarriesSubjectRecordAndID(t *testing.T) {
	r := work.Record{Kind: store.MessageInserted, Room: 42, Seq: 7, CommittedAt: committed}
	m := work.Message("work", 32, r)
	if want := "work.p" + strconv.Itoa(int(slotmap.Of(42))%32); m.Subject != want {
		t.Fatalf("subject %q, want %q", m.Subject, want)
	}
	if got := m.Header.Get(jetstream.MsgIDHeader); got != "m:42-0-7" {
		t.Fatalf("Nats-Msg-Id %q, want m:42-0-7", got)
	}
	got, err := work.Decode(m.Data)
	if err != nil || got.Room != 42 || got.Seq != 7 || !got.CommittedAt.Equal(committed) {
		t.Fatalf("payload decodes to %+v, %v", got, err)
	}
}
