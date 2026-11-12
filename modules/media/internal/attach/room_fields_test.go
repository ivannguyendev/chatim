package attach_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestPairsCarryTheRoomTheTabFromTheMessageMimeAndTheSendTime(t *testing.T) {
	r := newRig(t, false)
	png := "acme/an/20261108/aaaaaaaaaaaaaaaaaaaaaaaaaa"
	mp4 := "acme/an/20261108/bbbbbbbbbbbbbbbbbbbbbbbbbb"
	pdf := "acme/an/20261108/cccccccccccccccccccccccccc"
	html := "acme/an/20261108/dddddddddddddddddddddddddd"
	r.uploaded(t, png, mp4, pdf, html)
	m := ref("8812", 41)
	sent := now.Add(-time.Minute).Add(345 * time.Microsecond)
	r.chat.set(m, domain.MessageState{
		Found: true, Tenant: tenant, Keys: []string{png, mp4, pdf, html}, SentAt: sent,
		Mimes: map[string]string{png: "image/png", mp4: "video/mp4", pdf: "application/pdf", html: "text/html"},
	})
	for range 2 {
		if err := r.created(t, m); err != nil {
			t.Fatal(err)
		}
		r.wantTab(t, "8812", domain.RoomFileMedia, sent, mp4, png)
		r.wantTab(t, "8812", domain.RoomFileOther, sent, html, pdf)
	}
}

func (r *rig) wantTab(t *testing.T, room string, kind domain.RoomFileKind, sent time.Time, keys ...string) {
	t.Helper()
	got, err := r.files.ListRoom(t.Context(), domain.RoomFilesQuery{Tenant: tenant, Room: room, Kind: kind, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, p := range got {
		if !p.SentAt.Equal(sent.Truncate(time.Millisecond)) || p.Room != room {
			t.Fatalf("pair %+v, want room %s sent at %v", p, room, sent)
		}
		files = append(files, p.FileKey)
	}
	if !slices.Equal(files, keys) {
		t.Fatalf("%s tab = %v, want %v", kind, files, keys)
	}
}
