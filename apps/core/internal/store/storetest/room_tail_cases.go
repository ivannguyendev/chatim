package storetest

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type TailMessages interface {
	store.Messages
	store.RoomTails
}

type tailCase struct {
	name string
	run  func(t *testing.T, s TailMessages)
}

func RunRoomTails(t *testing.T, open func(t *testing.T) TailMessages) {
	t.Helper()
	for _, c := range []tailCase{
		{"tail window returns the newest main timeline messages after a seq, newest first, up to the limit", tailWindow},
		{"tail previews read whole messages across rooms in key order and skip missing keys", tailPreviews},
		{"tail reads reject invalid input", tailInvalid},
	} {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func tailTimeline(room, n uint64) []domain.Message {
	out := span(room, mainThread, 1, n)
	for i := range out {
		out[i].CreatedAt = baseTime.Add(time.Duration(i) * time.Second)
		out[i].From = []string{"alice", "bob", "carol"}[i%3]
		out[i].Deleted = i%4 == 3
		out[i].SkipUnread = i%5 == 4
		if out[i].Deleted {
			out[i].Text = ""
		}
	}
	return out
}

func tailOf(msgs []domain.Message) []domain.TailMessage {
	out := make([]domain.TailMessage, 0, len(msgs))
	for _, m := range slices.Backward(msgs) {
		out = append(out, domain.TailMessage{Seq: m.Seq, From: m.From, Deleted: m.Deleted, SkipUnread: m.SkipUnread, CreatedAt: m.CreatedAt})
	}
	return out
}

func sameTail(a, b domain.TailMessage) bool {
	at := a.CreatedAt.Equal(b.CreatedAt)
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	return at && a == b
}

func seedTails(t *testing.T, s TailMessages) (a, b []domain.Message) {
	t.Helper()
	a, b = tailTimeline(roomA, 12), tailTimeline(roomB, 4)
	mustInsert(t, s, slices.Concat(a, b, span(roomA, sideThread, 1, 20), span(roomA+2, mainThread, 1, 3)))
	return a, b
}

func tailWindow(t *testing.T, s TailMessages) {
	a, b := seedTails(t, s)
	cases := []struct {
		room, after uint64
		limit       int
		want        []domain.TailMessage
	}{
		{roomA, 0, 5, tailOf(a[7:])},
		{roomA, 0, domain.UnreadWindow, tailOf(a)},
		{roomA, 8, domain.UnreadWindow, tailOf(a[8:])},
		{roomA, 8, 2, tailOf(a[10:])},
		{roomA, 12, 5, nil},
		{roomB, 0, domain.UnreadWindow, tailOf(b)},
		{listMissingRoom, 0, 5, nil},
	}
	for _, c := range cases {
		got, err := s.TailWindow(t.Context(), c.room, c.after, c.limit)
		if err != nil {
			t.Fatalf("TailWindow(%d, after %d, limit %d): %v", c.room, c.after, c.limit, err)
		}
		if !slices.EqualFunc(got, c.want, sameTail) {
			t.Fatalf("TailWindow(%d, after %d, limit %d) = %+v,\nwant %+v", c.room, c.after, c.limit, got, c.want)
		}
	}
}

func tailPreviews(t *testing.T, s TailMessages) {
	a, b := seedTails(t, s)
	keys := []store.MsgKey{msgKey(roomB, mainThread, 4), msgKey(roomA, mainThread, 9), msgKey(roomA, mainThread, 99), msgKey(roomA, mainThread, 3)}
	got, err := s.TailPreviews(t.Context(), keys)
	if err != nil {
		t.Fatalf("TailPreviews: %v", err)
	}
	assertMessages(t, got, []domain.Message{a[2], a[8], b[3]})
	if got, err := s.TailPreviews(t.Context(), nil); err != nil || len(got) != 0 {
		t.Fatalf("TailPreviews(none) = %+v, %v; want none", got, err)
	}
}

func tailInvalid(t *testing.T, s TailMessages) {
	seedTails(t, s)
	many := make([]store.MsgKey, store.MaxTailPreviews+1)
	for i := range many {
		many[i] = msgKey(roomA, mainThread, uint64(i+1))
	}
	calls := map[string]func() error{
		"window limit 0":     func() error { _, err := s.TailWindow(t.Context(), roomA, 0, 0); return err },
		"window limit above": func() error { _, err := s.TailWindow(t.Context(), roomA, 0, domain.UnreadWindow+1); return err },
		"window room 0":      func() error { _, err := s.TailWindow(t.Context(), 0, 0, 5); return err },
		"window huge after":  func() error { _, err := s.TailWindow(t.Context(), roomA, math.MaxUint64, 5); return err },
		"previews seq 0": func() error {
			_, err := s.TailPreviews(t.Context(), []store.MsgKey{msgKey(roomA, mainThread, 0)})
			return err
		},
		"previews too many": func() error { _, err := s.TailPreviews(t.Context(), many); return err },
		"previews huge room": func() error {
			_, err := s.TailPreviews(t.Context(), []store.MsgKey{msgKey(math.MaxUint64, 0, 1)})
			return err
		},
	}
	for name, call := range calls {
		assertErrorIs(t, name, call(), apperr.ErrInvalidArgument)
	}
}
