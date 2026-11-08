package mongostore

import (
	"reflect"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
)

func messageWithFiles() domain.Message {
	m := sampleMessage()
	m.SkipUnread, m.Meta = true, []byte(`{"a":1}`)
	m.Attachments = []domain.Attachment{
		{Key: "acme/alice/20261107/a", Name: "ảnh.png", Mime: "image/png", Size: 2048, Dim: []int32{640, 480}, Dur: 3, Hash: "sha256:ab"},
		{Key: "acme/alice/20261107/b", Name: "hoa-don.pdf", Mime: "application/pdf", Size: 1},
	}
	return m
}

func TestMessageCodecWritesFilesLastWithShortNames(t *testing.T) {
	m := messageWithFiles()
	doc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "t", "f", "k", "x", "c", "ts", "nu", "m", "files"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	full, err := raw.Lookup("files", "0").Document().Elements()
	if err != nil {
		t.Fatalf("files.0: %v", err)
	}
	if got, want := elementKeys(full), []string{"key", "name", "mime", "size", "dim", "dur", "hash"}; !slices.Equal(got, want) {
		t.Fatalf("files.0 fields = %v, want %v", got, want)
	}
	bare, err := raw.Lookup("files", "1").Document().Elements()
	if err != nil {
		t.Fatalf("files.1: %v", err)
	}
	if got, want := elementKeys(bare), []string{"key", "name", "mime", "size"}; !slices.Equal(got, want) {
		t.Fatalf("files.1 fields = %v, want %v", got, want)
	}
	got, err := decodeMessage(back)
	if err != nil || !reflect.DeepEqual(got.Attachments, m.Attachments) {
		t.Fatalf("decoded attachments %+v, %v; want %+v", got.Attachments, err, m.Attachments)
	}
}

func TestMessageCodecOmitsFilesWhenThereAreNone(t *testing.T) {
	m := sampleMessage()
	m.Attachments = []domain.Attachment{}
	doc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if slices.Contains(fieldNames(t, raw), "files") {
		t.Fatalf("fields = %v, want no files", fieldNames(t, raw))
	}
	if got, err := decodeMessage(back); err != nil || got.Attachments != nil {
		t.Fatalf("decoded attachments %#v, %v; want nil", got.Attachments, err)
	}
}

func TestDeleteProjectionUnsetsFilesAndTextEditKeepsThem(t *testing.T) {
	update, err := editUpdate(domain.Edit{Kind: domain.EditDelete, At: codecTime}, 2)
	if err != nil {
		t.Fatalf("editUpdate: %v", err)
	}
	raw, err := bson.Marshal(update)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := bson.Raw(raw).LookupErr("$unset", "files"); err != nil {
		t.Fatalf("delete update %s has no $unset files", bson.Raw(raw))
	}
	keep, err := editUpdate(domain.Edit{Kind: domain.EditText, Text: "x", At: codecTime}, 2)
	if err != nil {
		t.Fatalf("editUpdate: %v", err)
	}
	raw, err = bson.Marshal(keep)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, op := range []string{"$set", "$unset"} {
		if _, err := bson.Raw(raw).LookupErr(op, "files"); err == nil {
			t.Fatalf("text edit update %s touches files in %s", bson.Raw(raw), op)
		}
	}
}

func elementKeys(elems []bson.RawElement) []string {
	out := make([]string, len(elems))
	for i, e := range elems {
		out[i] = e.Key()
	}
	return out
}
