package e2e_test

import (
	"strings"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const room, sender = "42", "alice"

func acks(n int) []e2e.Ack {
	out := make([]e2e.Ack, n)
	for i := range out {
		seq := uint64(i + 1)
		out[i] = e2e.Ack{CID: "a-" + string(rune('a'+i)), Seq: seq}
	}
	return out
}

func messagesOf(as []e2e.Ack) []*chatimv1.Message {
	out := make([]*chatimv1.Message, len(as))
	for i, a := range as {
		out[i] = &chatimv1.Message{RoomId: room, Seq: a.Seq, Cid: a.CID, Sender: sender, Text: e2e.TextFor(a.CID)}
	}
	return out
}

func expectErr(t *testing.T, err error, part string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), part) {
		t.Fatalf("err = %v, want one mentioning %q", err, part)
	}
}

func TestCheckAcksWantsContiguousSeqWithDistinctCIDs(t *testing.T) {
	if err := e2e.CheckAcks(acks(5)); err != nil {
		t.Fatalf("CheckAcks(valid) = %v", err)
	}
	gap := acks(5)
	gap[3].Seq = 9
	expectErr(t, e2e.CheckAcks(gap), "seq")
	dup := acks(5)
	dup[4].CID = dup[0].CID
	expectErr(t, e2e.CheckAcks(dup), "cid")
}

func TestCheckPageWantsExactlyTheAckedMessagesInSeqOrder(t *testing.T) {
	want := acks(4)
	if err := e2e.CheckPage(want, messagesOf(want), room, sender); err != nil {
		t.Fatalf("CheckPage(valid) = %v", err)
	}
	expectErr(t, e2e.CheckPage(want, messagesOf(want[:3]), room, sender), "3 messages")
	swapped := messagesOf(want)
	swapped[1], swapped[2] = swapped[2], swapped[1]
	expectErr(t, e2e.CheckPage(want, swapped, room, sender), "seq")
	cases := map[string]func(m *chatimv1.Message){
		"cid":    func(m *chatimv1.Message) { m.Cid = "other" },
		"text":   func(m *chatimv1.Message) { m.Text = "other" },
		"sender": func(m *chatimv1.Message) { m.Sender = "bob" },
		"room":   func(m *chatimv1.Message) { m.RoomId = "7" },
		"thread": func(m *chatimv1.Message) { m.ThreadRoot = 1 },
	}
	for field, spoil := range cases {
		got := messagesOf(want)
		spoil(got[2])
		expectErr(t, e2e.CheckPage(want, got, room, sender), field)
	}
}

func TestCheckEventsDedupesBySeqAndReportsWhatIsMissing(t *testing.T) {
	want := acks(5)
	var events []e2e.Event
	for _, a := range want[:4] {
		events = append(events, e2e.Event{Room: room, ID: e2e.MessageEventID(room, a.Seq), Seq: a.Seq, CID: a.CID})
	}
	events = append(events, events[1], events[1])
	cov, err := e2e.CheckEvents(want, room, events)
	if err != nil {
		t.Fatalf("CheckEvents = %v", err)
	}
	if cov.Distinct != 4 || cov.Duplicates != 2 || len(cov.Missing) != 1 || cov.Missing[0] != 5 {
		t.Fatalf("coverage = %+v, want 4 distinct, 2 duplicates, seq 5 missing", cov)
	}
	events = append(events, e2e.Event{Room: room, ID: e2e.MessageEventID(room, 5), Seq: 5, CID: want[4].CID})
	if cov, err := e2e.CheckEvents(want, room, events); err != nil || len(cov.Missing) != 0 {
		t.Fatalf("CheckEvents complete = %+v, %v", cov, err)
	}

	bad := map[string]e2e.Event{
		"unexpected seq": {Room: room, ID: e2e.MessageEventID(room, 6), Seq: 6, CID: "x"},
		"other room":     {Room: "7", ID: e2e.MessageEventID("7", 1), Seq: 1, CID: want[0].CID},
		"cid":            {Room: room, ID: e2e.MessageEventID(room, 2), Seq: 2, CID: "x"},
		"id":             {Room: room, ID: room + "-3", Seq: 3, CID: want[2].CID},
	}
	for part, ev := range bad {
		_, err := e2e.CheckEvents(want, room, append([]e2e.Event{ev}, events...))
		expectErr(t, err, part)
	}
}

func TestMissingListIsBounded(t *testing.T) {
	cov, err := e2e.CheckEvents(acks(26), room, nil)
	if err != nil || cov.MissingCount != 26 || len(cov.Missing) > 10 || cov.Missing[0] != 1 {
		t.Fatalf("coverage = %+v, %v; want 26 missing, at most 10 listed from seq 1", cov, err)
	}
}
