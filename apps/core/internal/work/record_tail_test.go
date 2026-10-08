package work_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestReactionRecordsCarryTheUserTail(t *testing.T) {
	if work.MaxRecordSize != 37+1+64 {
		t.Fatalf("MaxRecordSize = %d, want 102", work.MaxRecordSize)
	}
	for _, user := range []string{"a", "alice", strings.Repeat("Z", 64)} {
		r := work.Record{Kind: store.ReactionChanged, Room: 42, Thread: 3, Seq: 9, Version: 2, User: user, CommittedAt: committed}
		b := work.Encode(r)
		if len(b) != work.RecordSize+1+len(user) || int(b[work.RecordSize]) != len(user) || string(b[work.RecordSize+1:]) != user {
			t.Fatalf("Encode(user %q) = %x, want 37 bytes, the length byte and the user", user, b)
		}
		got, err := work.Decode(b)
		if err != nil || got.Kind != r.Kind || got.Room != 42 || got.Thread != 3 || got.Seq != 9 || got.Version != 2 || got.User != user || !got.CommittedAt.Equal(committed) {
			t.Fatalf("Decode(Encode(%+v)) = %+v, %v", r, got, err)
		}
	}
	pin := work.Encode(work.Record{Kind: store.PinInserted, Room: 42, Seq: 6, CommittedAt: committed})
	if got, err := work.Decode(pin); len(pin) != work.RecordSize || err != nil || got.Seq != 6 || got.User != "" {
		t.Fatalf("pin record = %d bytes, %+v, %v; want 37 bytes with seq 6", len(pin), got, err)
	}
}

func TestDecodeRejectsMalformedTails(t *testing.T) {
	reaction := work.Encode(work.Record{Kind: store.ReactionChanged, Room: 42, Seq: 9, Version: 1, User: "alice", CommittedAt: committed})
	room := work.Encode(work.Record{Kind: store.RoomInserted, Room: 42, CommittedAt: committed})
	badUser := slices.Clone(reaction)
	badUser[work.RecordSize+2] = '.'
	cases := map[string][]byte{
		"reaction without a user":      slices.Clone(reaction[:work.RecordSize]),
		"reaction with a bad user":     badUser,
		"tail of length 0":             append(slices.Clone(room), 0),
		"tail shorter than its length": append(slices.Clone(room), 3, 'a'),
		"tail over 64 bytes":           append(append(slices.Clone(room), 65), strings.Repeat("a", 65)...),
		"room record with a user":      append(slices.Clone(room), 1, 'a'),
	}
	for name, b := range cases {
		if _, err := work.Decode(b); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
			t.Errorf("%s: Decode = %v, want ErrBadRecord and not ErrUnknownKind", name, err)
		}
	}
}

func TestDecodeDefersWellFormedUnknownKinds(t *testing.T) {
	future := work.Encode(work.Record{Kind: store.RoomInserted, Room: 42, CommittedAt: committed})
	future[0] = 11
	for name, b := range map[string][]byte{"bare": future, "with a tail": append(slices.Clone(future), 2, 'a', 'b')} {
		_, err := work.Decode(b)
		if !errors.Is(err, work.ErrUnknownKind) || !errors.Is(err, work.ErrBadRecord) || !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: Decode = %v, want ErrUnknownKind", name, err)
		}
	}
	zero := slices.Clone(future)
	zero[0] = 0
	if _, err := work.Decode(zero); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
		t.Fatalf("Decode(kind 0) = %v, want a malformed record", err)
	}
}
