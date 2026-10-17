package mongostore

import (
	"bytes"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestMessageCodecWritesSkipUnreadAndMetaOnlyWhenSet(t *testing.T) {
	m := sampleMessage()
	m.Kind, m.SkipUnread, m.Meta = domain.KindSystem, true, []byte(`{"call_seconds":180}`)
	doc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "t", "f", "k", "x", "c", "ts", "nu", "m"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if k := raw.Lookup("k").AsInt64(); k != int64(domain.KindSystem) {
		t.Fatalf("k = %d, want %d", k, domain.KindSystem)
	}
	if typ := raw.Lookup("m").Type; typ != bson.TypeBinary {
		t.Fatalf("m type = %v, want binary", typ)
	}
	got, err := decodeMessage(back)
	if err != nil || got.Kind != domain.KindSystem || !got.SkipUnread || !bytes.Equal(got.Meta, m.Meta) {
		t.Fatalf("decodeMessage = %+v, %v; want a system message with skip unread and meta", got, err)
	}
	m.SkipUnread, m.Meta = false, []byte{}
	doc, _ = encodeMessage(m)
	if _, raw := roundTrip(t, doc); slices.Contains(fieldNames(t, raw), "nu") || slices.Contains(fieldNames(t, raw), "m") {
		t.Fatalf("fields = %v, want no nu or m", fieldNames(t, raw))
	}
}

func TestDeleteProjectionUnsetsMeta(t *testing.T) {
	update, err := editUpdate(domain.Edit{Kind: domain.EditDelete, At: codecTime}, 2)
	if err != nil {
		t.Fatalf("editUpdate: %v", err)
	}
	raw, err := bson.Marshal(update)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := bson.Raw(raw).LookupErr("$unset", "m"); err != nil {
		t.Fatalf("delete update %s has no $unset m", bson.Raw(raw))
	}
	keep, _ := editUpdate(domain.Edit{Kind: domain.EditText, Text: "x", At: codecTime}, 2)
	raw, _ = bson.Marshal(keep)
	if _, err := bson.Raw(raw).LookupErr("$unset", "m"); err == nil {
		t.Fatalf("text edit update %s unsets m", bson.Raw(raw))
	}
}
