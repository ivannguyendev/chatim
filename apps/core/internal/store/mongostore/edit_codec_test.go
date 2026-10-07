package mongostore

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func sampleEdit() domain.Edit {
	return domain.Edit{
		Room: 7_340_000_001, Thread: 3, Seq: 42, Version: 1, Kind: domain.EditText, Tenant: "acme", By: "alice",
		Text: "đã sửa", Prev: "xin chào", At: codecTime.Add(time.Minute),
	}
}

func TestEditCodecRoundTrip(t *testing.T) {
	e := sampleEdit()
	doc, err := encodeEdit(e)
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	if !bytes.Equal(doc.ID, keys.Edit(e.Room, e.Thread, e.Seq, e.Version)) || doc.Room != 7_340_000_001 {
		t.Fatalf("_id = %x, r = %d; want keys.Edit and the room", doc.ID, doc.Room)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "kind", "created_by", "text", "p", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodeEdit(back)
	if err != nil || !got.At.Equal(e.At) {
		t.Fatalf("decodeEdit = %+v, %v; want %+v", got, err, e)
	}
	got.At = e.At
	if got != e {
		t.Fatalf("decoded %+v, want %+v", got, e)
	}
}

func TestDeleteFactStoresNoText(t *testing.T) {
	e := sampleEdit()
	e.Version, e.Kind, e.Text, e.Prev = 2, domain.EditDelete, "", ""
	doc, err := encodeEdit(e)
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	_, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "kind", "created_by", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
}

func TestEncodeEditRejectsInvalid(t *testing.T) {
	for name, mutate := range map[string]func(*domain.Edit){
		"zero room":               func(e *domain.Edit) { e.Room = 0 },
		"zero seq":                func(e *domain.Edit) { e.Seq = 0 },
		"zero version":            func(e *domain.Edit) { e.Version = 0 },
		"version above max int32": func(e *domain.Edit) { e.Version = math.MaxInt32 + 1 },
		"zero kind":               func(e *domain.Edit) { e.Kind = 0 },
		"room above max int64":    func(e *domain.Edit) { e.Room = math.MaxInt64 + 1 },
		"seq above max int64":     func(e *domain.Edit) { e.Seq = math.MaxInt64 + 1 },
	} {
		e := sampleEdit()
		mutate(&e)
		if _, err := encodeEdit(e); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: encodeEdit = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestDecodeEditRejectsACorruptID(t *testing.T) {
	doc, err := encodeEdit(sampleEdit())
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	doc.ID = doc.ID[:24]
	if _, err := decodeEdit(doc); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeEdit(24-byte id) = %v, want errCorrupt", err)
	}
}

func TestMessageCodecCarriesEditStateButNotTheHiddenFlag(t *testing.T) {
	m := sampleMessage()
	m.Text, m.Version, m.Deleted, m.EditedAt = "", 3, true, codecTime.Add(time.Hour)
	stored := m
	m.Hidden = true
	doc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "t", "f", "k", "x", "c", "ts", "v", "d", "ea"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodeMessage(back)
	if err != nil || !got.CreatedAt.Equal(stored.CreatedAt) || !got.EditedAt.Equal(stored.EditedAt) {
		t.Fatalf("decodeMessage = %+v, %v; want %+v", got, err, stored)
	}
	got.CreatedAt, got.EditedAt = stored.CreatedAt, stored.EditedAt
	if !reflect.DeepEqual(got, stored) {
		t.Fatalf("decoded %+v, want %+v", got, stored)
	}
}

func TestMessageCodecRejectsOutOfRangeVersions(t *testing.T) {
	m := sampleMessage()
	m.Version = math.MaxInt32 + 1
	if _, err := encodeMessage(m); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("encodeMessage(version above max int32) = %v, want ErrInvalidArgument", err)
	}
	doc, err := encodeMessage(sampleMessage())
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	doc.Version = -1
	if _, err := decodeMessage(doc); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeMessage(negative version) = %v, want errCorrupt", err)
	}
}

func TestMemberCodecReadsClearedAt(t *testing.T) {
	doc := encodeMember(domain.Member{Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: codecTime}, 7_340_000_001)
	doc.ClearedAt = codecTime.Add(time.Minute)
	back, raw := roundTrip(t, doc)
	if got := fieldNames(t, raw); !slices.Contains(got, "cleared_at") {
		t.Fatalf("fields = %v, want cleared_at", got)
	}
	got, err := decodeMember(back)
	if err != nil || !got.ClearedAt.Equal(doc.ClearedAt) {
		t.Fatalf("decodeMember = %+v, %v; want cleared at %v", got, err, doc.ClearedAt)
	}
}
