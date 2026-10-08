package storetest

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func hiddenMark(user string, room, thread, seq uint64, sec int) domain.HiddenMessage {
	return domain.HiddenMessage{User: user, Room: room, Thread: thread, Seq: seq, At: baseTime.Add(time.Duration(sec) * time.Second)}
}

func mustHide(t *testing.T, s store.Hidden, marks ...domain.HiddenMessage) {
	t.Helper()
	for _, h := range marks {
		if _, err := s.Hide(t.Context(), h.User, msgKey(h.Room, h.Thread, h.Seq), h.At); err != nil {
			t.Fatalf("Hide(%+v): %v", h, err)
		}
	}
}

func sameHidden(a, b domain.HiddenMessage) bool {
	at := a.At.Equal(b.At)
	a.At, b.At = time.Time{}, time.Time{}
	return at && a == b
}

func assertHidden(t *testing.T, op string, got, want []domain.HiddenMessage) {
	t.Helper()
	if !slices.EqualFunc(got, want, sameHidden) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func hideKeepsFirstTime(t *testing.T, s editStores) {
	first := hiddenMark("bob", roomA, mainThread, 3, 1)
	again := first
	again.At = baseTime.Add(time.Hour)
	for i, h := range []domain.HiddenMessage{first, again} {
		fresh, err := s.hidden.Hide(t.Context(), h.User, msgKey(h.Room, h.Thread, h.Seq), h.At)
		if err != nil || fresh != (i == 0) {
			t.Fatalf("Hide #%d = %v, %v; want newly hidden %v", i+1, fresh, err, i == 0)
		}
	}
	got, err := s.hidden.Between(t.Context(), roomA, baseTime, baseTime.Add(2*time.Hour), store.MaxHiddenScan)
	if err != nil {
		t.Fatalf("Between: %v", err)
	}
	assertHidden(t, "Between", got, []domain.HiddenMessage{first})
}

func hiddenBetween(t *testing.T, s editStores) {
	before, atFrom := hiddenMark("bob", roomA, mainThread, 1, 0), hiddenMark("bob", roomA, mainThread, 9, 1)
	carolLow, carolHigh := hiddenMark("carol", roomA, mainThread, 4, 2), hiddenMark("carol", roomA, sideThread, 2, 2)
	bobSame, atTo := hiddenMark("bob", roomA, sideThread, 5, 2), hiddenMark("dave", roomA, mainThread, 2, 3)
	after, other := hiddenMark("bob", roomA, mainThread, 7, 4), hiddenMark("bob", roomB, mainThread, 1, 2)
	mustHide(t, s.hidden, after, carolHigh, other, atTo, before, carolLow, bobSame, atFrom)
	want := []domain.HiddenMessage{atFrom, bobSame, carolLow, carolHigh, atTo}
	from, to := baseTime.Add(time.Second), baseTime.Add(3*time.Second)
	cases := []struct {
		room     uint64
		from, to time.Time
		limit    int
		want     []domain.HiddenMessage
	}{
		{roomA, from, to, store.MaxHiddenScan, want},
		{roomA, from, to, 3, want[:3]},
		{roomB, from, to, 10, []domain.HiddenMessage{other}},
		{roomA, to.Add(time.Hour), to.Add(2 * time.Hour), 10, nil},
	}
	for _, c := range cases {
		got, err := s.hidden.Between(t.Context(), c.room, c.from, c.to, c.limit)
		if err != nil {
			t.Fatalf("Between(%d, %v, %v, %d): %v", c.room, c.from, c.to, c.limit, err)
		}
		assertHidden(t, fmt.Sprintf("Between(%d, limit %d)", c.room, c.limit), got, c.want)
	}
}

func hiddenInvalid(t *testing.T, s editStores) {
	for _, limit := range []int{0, store.MaxHiddenScan + 1} {
		_, err := s.hidden.Between(t.Context(), roomA, baseTime, baseTime.Add(time.Hour), limit)
		assertErrorIs(t, fmt.Sprintf("Between(limit %d)", limit), err, apperr.ErrInvalidArgument)
	}
	_, err := s.hidden.Hide(t.Context(), "bob", msgKey(roomA, mainThread, 1), time.Time{})
	assertErrorIs(t, "Hide(zero time)", err, apperr.ErrInvalidArgument)
	got, err := s.hidden.HiddenIn(t.Context(), "bob", roomA, mainThread, 1, 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("HiddenIn after a rejected hide = %v, %v; want none", got, err)
	}
}
