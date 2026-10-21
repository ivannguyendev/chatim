package wsclient

import (
	"slices"
	"strconv"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func created(room string, thread, seq uint64) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         room + "-" + strconv.FormatUint(thread, 10) + "-" + strconv.FormatUint(seq, 10),
		RoomId:     room,
		ThreadRoot: thread,
		Seq:        seq,
		Payload: &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{
			Message: &chatimv1.Message{RoomId: room, ThreadRoot: thread, Seq: seq},
		}},
	}
}

func edited(room string, seq uint64, ver uint32) *chatimv1.Event {
	return &chatimv1.Event{
		Id:      room + "-0-" + strconv.FormatUint(seq, 10) + "-v" + strconv.FormatUint(uint64(ver), 10),
		RoomId:  room,
		Seq:     seq,
		Payload: &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{Ver: ver}},
	}
}

func verdicts(a *Applier, events ...*chatimv1.Event) []Verdict {
	got := make([]Verdict, len(events))
	for i, ev := range events {
		got[i] = a.Apply(ev).Verdict
	}
	return got
}

func TestApplierDropsARepeatedEventID(t *testing.T) {
	a := NewApplier(0)
	room := &chatimv1.Event{Id: "1-created", RoomId: "1", Payload: &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{}}}
	got := verdicts(a, room, room, created("1", 0, 1), created("1", 0, 1))
	want := []Verdict{VerdictDeliver, VerdictDuplicate, VerdictDeliver, VerdictDuplicate}
	if !slices.Equal(got, want) {
		t.Fatalf("verdicts %v, want %v", got, want)
	}
	if stats := a.Stats(); stats.Delivered != 2 || stats.Duplicates != 2 {
		t.Fatalf("stats %+v, want 2 delivered and 2 duplicates", stats)
	}
}

func TestApplierForgetsTheOldestIDsBeyondItsWindow(t *testing.T) {
	a := NewApplier(2)
	hidden := func(id string) *chatimv1.Event {
		return &chatimv1.Event{Id: id, RoomId: "1", Payload: &chatimv1.Event_MessageHidden{MessageHidden: &chatimv1.MessageHidden{}}}
	}
	got := verdicts(a, hidden("a"), hidden("b"), hidden("c"), hidden("c"), hidden("a"))
	want := []Verdict{VerdictDeliver, VerdictDeliver, VerdictDeliver, VerdictDuplicate, VerdictDeliver}
	if !slices.Equal(got, want) {
		t.Fatalf("verdicts %v, want %v", got, want)
	}
}

func TestApplierGapCallsForTheMissingRangeOnly(t *testing.T) {
	a := NewApplier(0)
	steps := []struct {
		seq     uint64
		verdict Verdict
		gap     Gap
	}{
		{seq: 3},
		{seq: 4},
		{seq: 8, gap: Gap{RoomID: "1", From: 5, To: 7}},
		{seq: 6},
		{seq: 6, verdict: VerdictDuplicate},
		{seq: 8, verdict: VerdictDuplicate},
		{seq: 9},
	}
	for i, step := range steps {
		ev := created("1", 0, step.seq)
		ev.Id = ""
		got := a.Apply(ev)
		if got.Verdict != step.verdict || got.Gap != step.gap {
			t.Fatalf("step %d seq %d = %+v, want verdict %v gap %+v", i, step.seq, got, step.verdict, step.gap)
		}
	}
	if stats := a.Stats(); stats.Gaps != 1 || stats.Duplicates != 2 || stats.Delivered != 5 {
		t.Fatalf("stats %+v, want 1 gap, 2 duplicates, 5 delivered", stats)
	}
}

func TestApplierGapStartsAfterTheMarkedSeqAndKeepsThreadsApart(t *testing.T) {
	a := NewApplier(0)
	a.MarkSeq("1", 0, 10)
	a.MarkSeq("1", 0, 4)
	a.MarkSeq("1", 7, 0)
	if got := a.Apply(created("1", 0, 13)).Gap; got != (Gap{RoomID: "1", From: 11, To: 12}) {
		t.Fatalf("main timeline gap %+v, want 11..12", got)
	}
	if got := a.Apply(created("1", 7, 2)).Gap; got != (Gap{RoomID: "1", ThreadRoot: 7, From: 1, To: 1}) {
		t.Fatalf("thread gap %+v, want 1..1 on thread 7", got)
	}
	if got := a.Apply(created("2", 0, 50)).Gap; !got.Empty() {
		t.Fatalf("first message of an untracked room gap %+v, want none", got)
	}
}

func TestApplierLetsOnlyANewerVersionOfTheSameDocumentThrough(t *testing.T) {
	a := NewApplier(0)
	got := verdicts(a, edited("1", 5, 2), edited("1", 5, 1), edited("1", 6, 1), edited("1", 5, 3))
	want := []Verdict{VerdictDeliver, VerdictStale, VerdictDeliver, VerdictDeliver}
	if !slices.Equal(got, want) {
		t.Fatalf("verdicts %v, want %v", got, want)
	}
	if stats := a.Stats(); stats.Stale != 1 {
		t.Fatalf("stats %+v, want 1 stale", stats)
	}
}

func TestApplierRecordsTheVersionOfACreatedSnapshotWithoutGatingIt(t *testing.T) {
	a := NewApplier(0)
	republished := created("1", 0, 5)
	republished.GetMessageCreated().Message.Ver = 2
	got := verdicts(a, republished, edited("1", 5, 1), edited("1", 5, 2), edited("1", 5, 3))
	want := []Verdict{VerdictDeliver, VerdictStale, VerdictStale, VerdictDeliver}
	if !slices.Equal(got, want) {
		t.Fatalf("verdicts %v, want %v", got, want)
	}
}
