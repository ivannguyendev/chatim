package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func editActivityCases() []roomsCase {
	return []roomsCase{
		{"only an edited main timeline activity raises the last edit time, stamped by the server", activityLastEdit},
	}
}

const serverClockSkew = time.Second

func stampStart() time.Time { return time.Now().Add(-serverClockSkew).Truncate(time.Millisecond) }

func assertStamped(t *testing.T, what string, got, since time.Time) {
	t.Helper()
	if got.Before(since) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("%s = %v; want a server time from %v until now", what, got, since)
	}
}

func lastEdit(t *testing.T, s store.Rooms) time.Time {
	t.Helper()
	got, err := s.Get(t.Context(), roomA)
	if err != nil {
		t.Fatalf("Get(%d): %v", roomA, err)
	}
	return got.LastEditAt
}

func pastTick(stamp time.Time) { time.Sleep(time.Until(stamp.Add(2 * time.Millisecond))) }

func activityLastEdit(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	sent := secs(10)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 3, At: sent})
	if got := lastEdit(t, s); !got.IsZero() {
		t.Fatalf("last edit after a send = %v, want none", got)
	}
	since := stampStart()
	mustTouch(t, s, store.Activity{Room: roomA, At: secs(20), Edited: true})
	first := lastEdit(t, s)
	assertStamped(t, "last edit", first, since)
	assertActivity(t, s, roomA, 3, sent, since)
	pastTick(first)
	mustTouch(t, s, store.Activity{Room: roomA, Thread: sideThread, At: secs(40), Edited: true}, store.Activity{Room: roomA, At: secs(50)})
	if got := lastEdit(t, s); !got.Equal(first) {
		t.Fatalf("last edit after a thread edit and a plain change = %v, want %v kept", got, first)
	}
	pastTick(first)
	mustTouch(t, s, store.Activity{Room: roomA, At: first.Add(-5 * time.Millisecond), Edited: true})
	if second := lastEdit(t, s); !second.After(first) {
		t.Fatalf("last edit after an edit stamped behind the stored one = %v, want later than %v", second, first)
	}
}
