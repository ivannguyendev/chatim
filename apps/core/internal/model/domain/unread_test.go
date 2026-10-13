package domain_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

var unreadBase = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func tail(from, to uint64, sender string) []domain.TailMessage {
	out := make([]domain.TailMessage, 0, to-from+1)
	for s := to; s >= from; s-- {
		out = append(out, domain.TailMessage{Seq: s, From: sender, CreatedAt: unreadBase.Add(time.Duration(s) * time.Second)})
	}
	return out
}

func hidden(seqs ...uint64) map[uint64]struct{} {
	out := make(map[uint64]struct{}, len(seqs))
	for _, s := range seqs {
		out[s] = struct{}{}
	}
	return out
}

func TestCountUnreadExampleRoom777(t *testing.T) {
	window := tail(30, 45, "lan")
	window[45-43].From = "minh"
	got := domain.CountUnread(window, domain.RoomViewer{User: "minh", ReadSeq: 41, Hidden: hidden(44)})
	if got != (domain.Unread{Count: 2}) {
		t.Fatalf("CountUnread = %+v, want 2 exact", got)
	}
}

func TestCountUnreadSkipsWhatTheReaderShouldNotCount(t *testing.T) {
	window := tail(1, 10, "lan")
	window[0].Deleted = true
	window[1].SkipUnread = true
	window[2].From = "minh"
	cleared := unreadBase.Add(3 * time.Second)
	cases := []struct {
		name string
		v    domain.RoomViewer
		want domain.Unread
	}{
		{"everything after read", domain.RoomViewer{User: "minh"}, domain.Unread{Count: 7}},
		{"read up to 5", domain.RoomViewer{User: "minh", ReadSeq: 5}, domain.Unread{Count: 2}},
		{"cleared up to seq 3 counts strictly after", domain.RoomViewer{User: "minh", ClearedAt: cleared}, domain.Unread{Count: 4}},
		{"hidden", domain.RoomViewer{User: "minh", ReadSeq: 5, Hidden: hidden(6, 7)}, domain.Unread{}},
		{"read to the end", domain.RoomViewer{User: "minh", ReadSeq: 10}, domain.Unread{}},
		{"read past the end", domain.RoomViewer{User: "minh", ReadSeq: 99}, domain.Unread{}},
	}
	for _, c := range cases {
		if got := domain.CountUnread(window, c.v); got != c.want {
			t.Errorf("%s: CountUnread = %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := domain.CountUnread(nil, domain.RoomViewer{User: "minh"}); got != (domain.Unread{}) {
		t.Fatalf("empty room = %+v, want 0 exact", got)
	}
}

func TestCountUnreadStopsAt100(t *testing.T) {
	window := tail(1, 300, "lan")
	if got := domain.CountUnread(window, domain.RoomViewer{User: "minh"}); got != (domain.Unread{Count: 100}) {
		t.Fatalf("CountUnread = %+v, want 100 exact", got)
	}
	exactly := tail(1, 100, "lan")
	if got := domain.CountUnread(exactly, domain.RoomViewer{User: "minh"}); got != (domain.Unread{Count: 100}) {
		t.Fatalf("100 unread = %+v, want 100 exact", got)
	}
}

func TestCountUnreadApproxWhenTheWindowEndsBeforeTheReadPosition(t *testing.T) {
	mostlyOwn := func(from, to uint64) []domain.TailMessage {
		w := tail(from, to, "minh")
		for i := 0; i < len(w); i += 10 {
			w[i].From = "lan"
		}
		return w
	}
	cases := []struct {
		name    string
		window  []domain.TailMessage
		readSeq uint64
		want    domain.Unread
	}{
		{"window reaches the first unread", mostlyOwn(101, 400), 100, domain.Unread{Count: 30}},
		{"one message beyond the window", mostlyOwn(102, 401), 100, domain.Unread{Count: 30, Approx: true}},
		{"far behind", mostlyOwn(701, 1000), 0, domain.Unread{Count: 30, Approx: true}},
		{"short window is the whole room", mostlyOwn(5, 200), 0, domain.Unread{Count: 20}},
		{"longer slice is cut at 300", mostlyOwn(101, 500), 100, domain.Unread{Count: 30, Approx: true}},
	}
	for _, c := range cases {
		if got := domain.CountUnread(c.window, domain.RoomViewer{User: "minh", ReadSeq: c.readSeq}); got != c.want {
			t.Errorf("%s: CountUnread = %+v, want %+v", c.name, got, c.want)
		}
	}
}
