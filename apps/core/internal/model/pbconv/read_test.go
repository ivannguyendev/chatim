package pbconv_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestHiddenAndClearedEventsCarryTheUserAndTheirIDs(t *testing.T) {
	clearedAt := sentAt.Add(3_123_000_000)
	wantHidden := memberEnvelope("9007199254740993-hd-bob-3-9", "bob")
	wantHidden.ThreadRoot, wantHidden.Seq = 3, 9
	wantHidden.Payload = &chatimv1.Event_MessageHidden{MessageHidden: &chatimv1.MessageHidden{User: "bob", ThreadRoot: 3, Seq: 9}}
	if got := pbconv.MessageHidden(groupRoom(), "bob", 3, 9, changedAt); !proto.Equal(got, wantHidden) {
		t.Fatalf("MessageHidden = %v, want %v", got, wantHidden)
	}
	wantCleared := memberEnvelope(pbconv.ClearedEventID(groupRoom().ID, "bob", clearedAt), "bob")
	wantCleared.Payload = &chatimv1.Event_HistoryCleared{HistoryCleared: &chatimv1.HistoryCleared{User: "bob", ClearedAt: timestamppb.New(clearedAt)}}
	if got := pbconv.HistoryCleared(groupRoom(), "bob", clearedAt, changedAt); !proto.Equal(got, wantCleared) {
		t.Fatalf("HistoryCleared = %v, want %v", got, wantCleared)
	}
}

func readEntries(lines ...string) []*chatimv1.ReadEntry {
	out := make([]*chatimv1.ReadEntry, 0, len(lines))
	for _, l := range lines {
		var e chatimv1.ReadEntry
		parts := strings.Split(l, ":")
		e.User = parts[0]
		e.ReadSeq, _ = strconv.ParseUint(parts[1], 10, 64)
		e.ReadVer, _ = strconv.ParseUint(parts[2], 10, 64)
		out = append(out, &e)
	}
	return out
}

func TestReadPositionsCarriesSortedEntriesUnderAContentID(t *testing.T) {
	sum := sha256.Sum256([]byte("alice:4:2\nbob:41:6"))
	want := memberEnvelope("9007199254740993-rp-"+hex.EncodeToString(sum[:])[:16], "")
	want.Payload = &chatimv1.Event_ReadPositions{ReadPositions: &chatimv1.ReadPositions{Entries: readEntries("alice:4:2", "bob:41:6")}}
	got := pbconv.ReadPositions(groupRoom(), readEntries("bob:41:6", "alice:4:2"), changedAt)
	if !proto.Equal(got, want) {
		t.Fatalf("ReadPositions = %v, want %v", got, want)
	}
	if got.GetThreadRoot() != 0 || got.GetSeq() != 0 || got.GetActor() != "" {
		t.Fatalf("thread/seq/actor = %d/%d/%q, want 0/0/empty", got.GetThreadRoot(), got.GetSeq(), got.GetActor())
	}
}

func TestReadPositionsIDFollowsTheContent(t *testing.T) {
	room := groupRoom().ID
	base := pbconv.ReadPositionsEventID(room, readEntries("alice:4:2", "bob:41:6"))
	if again := pbconv.ReadPositionsEventID(room, readEntries("bob:41:6", "alice:4:2")); again != base {
		t.Fatalf("same content in another order = %q, want %q", again, base)
	}
	for name, other := range map[string]string{
		"read_seq": pbconv.ReadPositionsEventID(room, readEntries("alice:4:2", "bob:42:6")),
		"read_ver": pbconv.ReadPositionsEventID(room, readEntries("alice:4:2", "bob:41:7")),
		"user":     pbconv.ReadPositionsEventID(room, readEntries("alice:4:2", "carol:41:6")),
		"room":     pbconv.ReadPositionsEventID(room+1, readEntries("alice:4:2", "bob:41:6")),
	} {
		if other == base {
			t.Errorf("a different %s kept the id %q", name, base)
		}
	}
}
