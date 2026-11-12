package storetest

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
)

const (
	roomA = "8812"
	roomB = "9001"
)

func roomFileCases() []storeCase {
	return []storeCase{
		{"list room gives the live files of one room and tab newest first", listRoomOneTab},
		{"files of one message page by key without a gap or a repeat", listRoomPagesTies},
		{"cleared at hides the files sent at or before it, with or without a cursor", listRoomCleared},
		{"another tenant sees nothing of the same room", listRoomTenants},
		{"a cursor taken in another room only lists the asked room", listRoomForeignCursor},
		{"attach again fills the room fields of an older pair", listRoomFillsOldPair},
		{"send times below a millisecond page whole", listRoomSubMillisecond},
	}
}

func sentAt(minutes int) time.Time { return created.Add(time.Duration(minutes) * time.Minute) }

func RoomPair(tenant, room string, seq uint64, file string, kind domain.RoomFileKind, at time.Time) domain.MessageFile {
	p := Pair(tenant, domain.MessageRef{Room: room, Seq: seq}.Key(), file)
	p.Room, p.Kind, p.SentAt = room, kind, at
	return p
}

func listRoom(t *testing.T, s store.MessageFiles, q domain.RoomFilesQuery) []domain.MessageFile {
	t.Helper()
	if q.Limit == 0 {
		q.Limit = 100
	}
	got, err := s.ListRoom(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func pairNames(pairs []domain.MessageFile) []string {
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.MessageKey + "|" + p.FileKey
	}
	return out
}

func wantNames(t *testing.T, got []domain.MessageFile, want ...domain.MessageFile) {
	t.Helper()
	if g, w := pairNames(got), pairNames(want); !slices.Equal(g, w) {
		t.Fatalf("listed %v, want %v", g, w)
	}
}

func mediaQuery(tenant, room string) domain.RoomFilesQuery {
	return domain.RoomFilesQuery{Tenant: tenant, Room: room, Kind: domain.RoomFileMedia}
}

func listRoomOneTab(t *testing.T, s Stores) {
	png := RoomPair("acme", roomA, 41, "acme/an/20261111/a", domain.RoomFileMedia, sentAt(1))
	pdf := RoomPair("acme", roomA, 42, "acme/an/20261111/b", domain.RoomFileOther, sentAt(2))
	jpg := RoomPair("acme", roomA, 43, "acme/an/20261111/c", domain.RoomFileMedia, sentAt(3))
	elsewhere := RoomPair("acme", roomB, 43, "acme/an/20261111/d", domain.RoomFileMedia, sentAt(3))
	gone := RoomPair("acme", roomA, 44, "acme/an/20261111/e", domain.RoomFileMedia, sentAt(4))
	attach(t, s.Files, png, pdf, jpg, elsewhere, gone)
	release(t, s.Files, "acme", gone.MessageKey)
	got := listRoom(t, s.Files, mediaQuery("acme", roomA))
	wantNames(t, got, jpg, png)
	if got[0].Tenant != "acme" || got[0].Room != roomA || got[0].Kind != domain.RoomFileMedia || !got[0].SentAt.Equal(sentAt(3)) || got[0].State != domain.PairLive {
		t.Fatalf("first pair = %+v", got[0])
	}
	wantNames(t, listRoom(t, s.Files, domain.RoomFilesQuery{Tenant: "acme", Room: roomA, Kind: domain.RoomFileOther}), pdf)
}

func pageAll(t *testing.T, s store.MessageFiles, q domain.RoomFilesQuery) []domain.MessageFile {
	t.Helper()
	var all []domain.MessageFile
	for range 10 {
		page := listRoom(t, s, q)
		all = append(all, page...)
		if len(page) < q.Limit {
			return all
		}
		next, err := domain.ParseRoomFileCursor(domain.CursorOf(page[len(page)-1]).Encode())
		if err != nil {
			t.Fatal(err)
		}
		q.Before = next
	}
	t.Fatal("paging did not end")
	return nil
}

func listRoomPagesTies(t *testing.T, s Stores) {
	older := RoomPair("acme", roomA, 49, "acme/an/20261111/z", domain.RoomFileMedia, sentAt(1))
	a := RoomPair("acme", roomA, 50, "acme/an/20261111/a", domain.RoomFileMedia, sentAt(2))
	b := RoomPair("acme", roomA, 50, "acme/an/20261111/b", domain.RoomFileMedia, sentAt(2))
	c := RoomPair("acme", roomA, 50, "acme/an/20261111/c", domain.RoomFileMedia, sentAt(2))
	attach(t, s.Files, older, a, b, c)
	q := mediaQuery("acme", roomA)
	q.Limit = 2
	wantNames(t, pageAll(t, s.Files, q), c, b, a, older)
}

func listRoomCleared(t *testing.T, s Stores) {
	first := RoomPair("acme", roomA, 41, "acme/an/20261111/a", domain.RoomFileMedia, sentAt(1))
	second := RoomPair("acme", roomA, 42, "acme/an/20261111/b", domain.RoomFileMedia, sentAt(2))
	third := RoomPair("acme", roomA, 43, "acme/an/20261111/c", domain.RoomFileMedia, sentAt(3))
	attach(t, s.Files, first, second, third)
	q := mediaQuery("acme", roomA)
	q.ClearedAt = sentAt(2)
	wantNames(t, listRoom(t, s.Files, q), third)
	q.ClearedAt, q.Before = sentAt(1), domain.CursorOf(third)
	wantNames(t, listRoom(t, s.Files, q), second)
}

func listRoomTenants(t *testing.T, s Stores) {
	mine := RoomPair("acme", roomA, 41, fileX, domain.RoomFileMedia, sentAt(1))
	theirs := RoomPair("other", roomA, 41, foreignFile, domain.RoomFileMedia, sentAt(1))
	attach(t, s.Files, mine, theirs)
	wantNames(t, listRoom(t, s.Files, mediaQuery("acme", roomA)), mine)
	wantNames(t, listRoom(t, s.Files, mediaQuery("other", roomA)), theirs)
	wantNames(t, listRoom(t, s.Files, mediaQuery("third", roomA)))
	q := mediaQuery("other", roomA)
	q.Before = domain.RoomFileCursor{SentAt: sentAt(9), MessageKey: mine.MessageKey, FileKey: mine.FileKey}
	wantNames(t, listRoom(t, s.Files, q), theirs)
}

func listRoomForeignCursor(t *testing.T, s Stores) {
	a1 := RoomPair("acme", roomA, 41, "acme/an/20261111/a", domain.RoomFileMedia, sentAt(1))
	a2 := RoomPair("acme", roomA, 42, "acme/an/20261111/b", domain.RoomFileMedia, sentAt(5))
	b1 := RoomPair("acme", roomB, 7, "acme/an/20261111/c", domain.RoomFileMedia, sentAt(3))
	attach(t, s.Files, a1, a2, b1)
	q := mediaQuery("acme", roomA)
	q.Before = domain.CursorOf(b1)
	got := listRoom(t, s.Files, q)
	wantNames(t, got, a1)
	for _, p := range got {
		if !strings.HasPrefix(p.MessageKey, roomA+"-") {
			t.Fatalf("listed %s of another room", p.MessageKey)
		}
	}
}

func listRoomFillsOldPair(t *testing.T, s Stores) {
	attach(t, s.Files, Pair("acme", domain.MessageRef{Room: roomA, Seq: 41}.Key(), fileX))
	wantNames(t, listRoom(t, s.Files, mediaQuery("acme", roomA)))
	filled := RoomPair("acme", roomA, 41, fileX, domain.RoomFileMedia, sentAt(1))
	attach(t, s.Files, filled)
	wantNames(t, listRoom(t, s.Files, mediaQuery("acme", roomA)), filled)
}

func listRoomSubMillisecond(t *testing.T, s Stores) {
	at := sentAt(1).Add(512_345 * time.Nanosecond)
	a := RoomPair("acme", roomA, 41, "acme/an/20261111/a", domain.RoomFileMedia, at)
	b := RoomPair("acme", roomA, 41, "acme/an/20261111/b", domain.RoomFileMedia, at)
	attach(t, s.Files, a, b)
	q := mediaQuery("acme", roomA)
	q.Limit = 1
	all := pageAll(t, s.Files, q)
	wantNames(t, all, b, a)
	if !all[0].SentAt.Equal(at.Truncate(time.Millisecond)) {
		t.Fatalf("sent at = %v, want %v", all[0].SentAt, at.Truncate(time.Millisecond))
	}
}
