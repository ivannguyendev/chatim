package mongostore

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var codecTime = time.UnixMilli(1_700_000_000_123).UTC()

func sampleMessage() domain.Message {
	return domain.Message{
		Room: 7_340_000_001, Thread: 3, Seq: 42, Tenant: "acme", From: "alice",
		Kind: domain.KindText, Text: "xin chào", CID: "cid-1", CreatedAt: codecTime,
	}
}

func roundTrip[D any](t *testing.T, doc D) (D, bson.Raw) {
	t.Helper()
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out D
	if err := bson.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return out, raw
}

func fieldNames(t *testing.T, raw bson.Raw) []string {
	t.Helper()
	elems, err := raw.Elements()
	if err != nil {
		t.Fatalf("Elements: %v", err)
	}
	names := make([]string, len(elems))
	for i, e := range elems {
		names[i] = e.Key()
	}
	return names
}

func TestMessageCodecRoundTrip(t *testing.T) {
	m := sampleMessage()
	doc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	if !bytes.Equal(doc.ID, keys.Msg(m.Room, m.Thread, m.Seq)) {
		t.Fatalf("_id = %x, want keys.Msg", doc.ID)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "t", "f", "k", "x", "c", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodeMessage(back)
	if err != nil {
		t.Fatalf("decodeMessage: %v", err)
	}
	if !got.CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, m.CreatedAt)
	}
	got.CreatedAt = m.CreatedAt
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("decoded %+v, want %+v", got, m)
	}
}

func TestEncodeMessageRejectsInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.Message)
	}{
		{"zero room", func(m *domain.Message) { m.Room = 0 }},
		{"zero seq", func(m *domain.Message) { m.Seq = 0 }},
		{"seq above max int64", func(m *domain.Message) { m.Seq = math.MaxInt64 + 1 }},
		{"reserved max seq", func(m *domain.Message) { m.Seq = math.MaxUint64 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := sampleMessage()
			tt.mutate(&m)
			if _, err := encodeMessage(m); !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("encodeMessage error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestEncodeMessageAcceptsMaxInt64(t *testing.T) {
	m := sampleMessage()
	m.Seq = math.MaxInt64
	doc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	got, err := decodeMessage(doc)
	if err != nil || got.Seq != m.Seq {
		t.Fatalf("decode = %+v, %v; want seq %d", got, err, int64(math.MaxInt64))
	}
}

func TestDecodeMessageRejectsCorruptDocument(t *testing.T) {
	good, err := encodeMessage(sampleMessage())
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	shortID := good
	shortID.ID = shortID.ID[:16]
	for name, doc := range map[string]messageDoc{"short id": shortID} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeMessage(doc); err == nil {
				t.Fatal("decodeMessage accepted a corrupt document")
			}
		})
	}
}

func TestRoomCodecRoundTrip(t *testing.T) {
	r := domain.Room{ID: 7_340_000_001, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 2, MemberCountVer: 3}
	doc, err := encodeRoom(r)
	if err != nil {
		t.Fatalf("encodeRoom: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "tenant", "type", "name", "created_by", "created_at", "member_count", "member_count_ver"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodeRoom(back)
	if err != nil || !got.CreatedAt.Equal(r.CreatedAt) {
		t.Fatalf("decodeRoom = %+v, %v; want %+v", got, err, r)
	}
	got.CreatedAt = r.CreatedAt
	if got != r {
		t.Fatalf("decoded %+v, want %+v", got, r)
	}
}

func TestRoomCodecDecodesActivity(t *testing.T) {
	d := roomDoc{
		ID: 7_340_000_001, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 2,
		LastSeq: 42, LastMsgAt: codecTime.Add(time.Minute), LastChangeAt: codecTime.Add(2 * time.Minute),
	}
	got, err := decodeRoom(d)
	if err != nil || got.LastSeq != 42 || !got.LastMsgAt.Equal(d.LastMsgAt) || !got.LastChangeAt.Equal(d.LastChangeAt) {
		t.Fatalf("decodeRoom = %+v, %v; want seq 42 at %v / %v", got, err, d.LastMsgAt, d.LastChangeAt)
	}
	d.LastSeq = -1
	if _, err := decodeRoom(d); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeRoom(negative last seq) = %v, want errCorrupt", err)
	}
}
