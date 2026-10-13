package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func editActivityCases() []roomsCase {
	return []roomsCase{
		{"only an edited main timeline activity raises the last edit time, never back", activityLastEdit},
	}
}

func assertLastEdit(t *testing.T, s store.Rooms, want time.Time) {
	t.Helper()
	got, err := s.Get(t.Context(), roomA)
	if err != nil || !got.LastEditAt.Equal(want) {
		t.Fatalf("Get(%d) last edit = %v, %v; want %v", roomA, got.LastEditAt, err, want)
	}
}

func activityLastEdit(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	sent, edit, react, older := secs(10), secs(20), secs(30), secs(15)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 3, At: sent})
	assertLastEdit(t, s, time.Time{})
	mustTouch(t, s, store.Activity{Room: roomA, At: edit, Edited: true})
	assertLastEdit(t, s, edit)
	assertActivity(t, s, roomA, 3, sent, edit)
	mustTouch(t, s, store.Activity{Room: roomA, At: older, Edited: true}, store.Activity{Room: roomA, At: react})
	assertLastEdit(t, s, edit)
	assertActivity(t, s, roomA, 3, sent, react)
	mustTouch(t, s, store.Activity{Room: roomA, Thread: sideThread, At: secs(40), Edited: true})
	assertLastEdit(t, s, edit)
	mustTouch(t, s, store.Activity{Room: roomA, At: secs(50), Edited: true})
	assertLastEdit(t, s, secs(50))
}
