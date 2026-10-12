package mongostore

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func sampleEdit() domain.Edit {
	return domain.Edit{
		Room: 7_340_000_001, Thread: 3, Seq: 42, Version: 1, Kind: domain.EditText, Tenant: "acme", By: "alice",
		Text: "đã sửa", At: codecTime.Add(time.Minute),
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
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "kind", "created_by", "text", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodeEdit(back)
	if err != nil || !got.At.Equal(e.At) {
		t.Fatalf("decodeEdit = %+v, %v; want %+v", got, err, e)
	}
	got.At = e.At
	if !reflect.DeepEqual(got, e) {
		t.Fatalf("decoded %+v, want %+v", got, e)
	}
}

var editMinh = domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}

func TestEditCodecCarriesMentionsUnderFullNames(t *testing.T) {
	e := sampleEdit()
	e.Mentions, e.MentionAll = []domain.MentionTarget{editMinh, {Kind: domain.MentionGroup, ID: "team:ops"}}, true
	doc, err := encodeEdit(e)
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	back, raw := roundTrip(t, doc)
	want := []string{"_id", "room_id", "tenant", "kind", "created_by", "text", "created_at", "mention_targets", "mention_all"}
	if got := fieldNames(t, raw); !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if first := raw.Lookup("mention_targets", "0", "kind").StringValue(); first != "user" {
		t.Fatalf("mention_targets.0.kind = %q, want user", first)
	}
	got, err := decodeEdit(back)
	if err != nil || !slices.Equal(got.Mentions, e.Mentions) || !got.MentionAll {
		t.Fatalf("decodeEdit = %+v, %v; want mentions %v and @all", got, err, e.Mentions)
	}
}

func TestDeleteFactStoresNoText(t *testing.T) {
	e := sampleEdit()
	e.Version, e.Kind, e.Text = 2, domain.EditDelete, ""
	doc, err := encodeEdit(e)
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	_, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "kind", "created_by", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
}

func TestOriginalRowRoundTripsAtVerZero(t *testing.T) {
	e := sampleEdit()
	e.Version, e.Kind, e.Text = 0, domain.EditOriginal, "xin chào"
	doc, err := encodeEdit(e)
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "kind", "created_by", "text", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if got, err := decodeEdit(back); err != nil || got.Version != 0 || got.Kind != domain.EditOriginal || got.Text != "xin chào" {
		t.Fatalf("decodeEdit = %+v, %v; want the original row", got, err)
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
		"duplicate mention":       func(e *domain.Edit) { e.Mentions = []domain.MentionTarget{editMinh, editMinh} },
		"@all as a target":        func(e *domain.Edit) { e.Mentions = []domain.MentionTarget{{Kind: domain.MentionAll}} },
		"delete with @all":        func(e *domain.Edit) { e.Kind, e.Version, e.MentionAll = domain.EditDelete, 2, true },
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
