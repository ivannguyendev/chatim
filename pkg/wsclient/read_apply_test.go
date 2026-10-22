package wsclient

import (
	"slices"
	"strconv"
	"testing"
	"testing/synctest"

	"github.com/lxzan/gws"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func readLines(entries []*chatimv1.ReadEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.GetUser() + ":" + strconv.FormatUint(e.GetReadVer(), 10)
	}
	return out
}

func TestApplierKeepsTheLargerReadVerPerRoomAndUser(t *testing.T) {
	a := NewApplier(0)
	first := a.Apply(readsOf("7", "ann", "3", "bob", "2"))
	if first.Verdict != VerdictDeliver || !slices.Equal(readLines(first.Reads), []string{"ann:3", "bob:2"}) {
		t.Fatalf("first = %+v, want both entries", first)
	}
	late := a.Apply(readsOf("7", "ann", "2", "bob", "4"))
	if late.Verdict != VerdictDeliver || !slices.Equal(readLines(late.Reads), []string{"bob:4"}) {
		t.Fatalf("mixed = %+v, want only bob's newer entry", late)
	}
	if stale := a.Apply(readsOf("7", "ann", "1", "bob", "4")); stale.Verdict != VerdictStale || stale.Reads != nil {
		t.Fatalf("all older = %+v, want stale", stale)
	}
	if other := a.Apply(readsOf("8", "ann", "1")); other.Verdict != VerdictDeliver || len(other.Reads) != 1 {
		t.Fatalf("other room = %+v, want delivered", other)
	}
	if again := a.Apply(readsOf("7", "ann", "3", "bob", "2")); again.Verdict != VerdictDuplicate {
		t.Fatalf("same event again = %+v, want duplicate", again)
	}
	if s := a.Stats(); s.Delivered != 3 || s.Stale != 1 || s.Duplicates != 1 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestClientHandsOnlyNewerReadEntriesToTheEventHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan *chatimv1.Event, 4)
		var conn *gws.Conn
		server := newFakeServer(func(c *gws.Conn, _ *chatimv1.ClientFrame) { conn = c })
		client := dialPipe(t, server, "token", OnEvent(func(ev *chatimv1.Event) { events <- ev }))
		synctest.Wait()
		for _, ev := range []*chatimv1.Event{readsOf("7", "ann", "3"), readsOf("7", "ann", "2", "bob", "1"), readsOf("7", "ann", "1")} {
			writeServerFrame(t, conn, &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Event{Event: ev}})
		}
		synctest.Wait()
		var got [][]string
		for len(events) > 0 {
			got = append(got, readLines((<-events).GetReadPositions().GetEntries()))
		}
		if len(got) != 2 || !slices.Equal(got[0], []string{"ann:3"}) || !slices.Equal(got[1], []string{"bob:1"}) {
			t.Fatalf("handled entries = %v, want ann:3 then bob:1 only", got)
		}
		_ = client.Close()
	})
}
