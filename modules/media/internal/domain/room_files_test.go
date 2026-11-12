package domain_test

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestRoomFileKindSplitsImagesAndVideosFromOtherFiles(t *testing.T) {
	cases := map[string]domain.RoomFileKind{
		"image/png":       domain.RoomFileMedia,
		"image/svg+xml":   domain.RoomFileMedia,
		"video/mp4":       domain.RoomFileMedia,
		"application/pdf": domain.RoomFileOther,
		"text/html":       domain.RoomFileOther,
		"audio/mpeg":      domain.RoomFileOther,
		"":                domain.RoomFileOther,
	}
	for mime, want := range cases {
		if got := domain.RoomFileKindOf(mime); got != want {
			t.Fatalf("kind of %q = %q, want %q", mime, got, want)
		}
	}
	if !domain.RoomFileMedia.Valid() || !domain.RoomFileOther.Valid() || domain.RoomFileKind("").Valid() || domain.RoomFileKind("image").Valid() {
		t.Fatal("only media and file are valid kinds")
	}
}

func TestRoomFileCursorRoundTripsInMilliseconds(t *testing.T) {
	sent := time.Date(2026, 11, 11, 9, 1, 0, 512_345_678, time.UTC)
	pair := domain.MessageFile{MessageKey: "9036108543124204147-0-41", FileKey: "acme/an/20261111/0cq5|odd", SentAt: sent}
	c := domain.CursorOf(pair)
	got, err := domain.ParseRoomFileCursor(c.Encode())
	if err != nil {
		t.Fatal(err)
	}
	want := domain.RoomFileCursor{SentAt: sent.Truncate(time.Millisecond), MessageKey: pair.MessageKey, FileKey: pair.FileKey}
	if !got.SentAt.Equal(want.SentAt) || got.MessageKey != want.MessageKey || got.FileKey != want.FileKey {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
	empty, err := domain.ParseRoomFileCursor("")
	if err != nil || !empty.IsZero() || empty.Encode() != "" {
		t.Fatalf("empty cursor = %+v, %v", empty, err)
	}
}

func TestBrokenRoomFileCursorsAreInvalid(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for _, s := range []string{
		"not base64!",
		enc("1790000000000|8812-0-41"),
		enc("soon|8812-0-41|acme/an/x"),
		enc("-5|8812-0-41|acme/an/x"),
		enc("1790000000000||acme/an/x"),
		enc("1790000000000|8812-0-41|"),
	} {
		if _, err := domain.ParseRoomFileCursor(s); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("cursor %q: err = %v, want invalid", s, err)
		}
	}
}

func TestMessageKeyParsesBackToItsRef(t *testing.T) {
	ref := domain.MessageRef{Room: "9036108543124204147", Thread: 0, Seq: 41}
	got, err := domain.ParseMessageKey(ref.Key())
	if err != nil || got != ref {
		t.Fatalf("parse %q = %+v, %v", ref.Key(), got, err)
	}
	for _, bad := range []string{"", "8812", "8812-0", "8812-x-41", "8812-0-0", "a-0-41", "8812-0-41-2"} {
		if _, err := domain.ParseMessageKey(bad); err == nil {
			t.Fatalf("parse %q succeeded", bad)
		}
	}
}
