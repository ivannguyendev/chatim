package mutate_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func previousOf(ev *chatimv1.Event) *chatimv1.MentionSet {
	if d := ev.GetMessageDeleted(); d != nil {
		return d.GetPreviousMentions()
	}
	return ev.GetMessageEdited().GetPreviousMentions()
}

func assertPrevious(t *testing.T, ev *chatimv1.Event, want domain.Mentions) {
	t.Helper()
	if got := previousOf(ev); !proto.Equal(got, pbconv.MentionSet(want)) {
		t.Fatalf("event %s previous_mentions = %v, want %v", ev.GetId(), got, pbconv.MentionSet(want))
	}
}

func TestChangeEventsCarryTheTargetsOfTheVersionBefore(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendStamped(t, 1, minh)
	if _, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", false, minh, lan)); err != nil {
		t.Fatalf("Edit v1: %v", err)
	}
	v1At := rg.at()
	rg.now = rg.now.Add(time.Minute)
	if _, err := rg.m.Edit(t.Context(), mentionEdit(1, "v2", true, lan)); err != nil {
		t.Fatalf("Edit v2: %v", err)
	}
	v2At := rg.at()
	rg.now = rg.now.Add(time.Minute)
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 2)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, events := rg.events.list()
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	assertPrevious(t, events[0], domain.Mentions{Targets: []domain.MentionTarget{stamped(minh, created)}, All: true, AllAt: created})
	assertPrevious(t, events[1], domain.Mentions{Targets: []domain.MentionTarget{stamped(minh, created), stamped(lan, v1At)}})
	assertPrevious(t, events[2], domain.Mentions{Targets: []domain.MentionTarget{stamped(lan, v1At)}, All: true, AllAt: v2At})
}

func TestRetriedChangeRepeatsThePreviousTargets(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendStamped(t, 1, minh)
	if _, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", false, lan)); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	rg.now = rg.now.Add(time.Hour)
	if _, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", false, lan)); err != nil {
		t.Fatalf("retried Edit: %v", err)
	}
	_, events := rg.events.list()
	if len(events) != 2 || !proto.Equal(events[0], events[1]) {
		t.Fatalf("events = %v, want the same event twice", events)
	}
	assertPrevious(t, events[1], domain.Mentions{Targets: []domain.MentionTarget{stamped(minh, created)}, All: true, AllAt: created})
}

func TestRetriedDeleteRepeatsTheDeletedTargets(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendStamped(t, 1, minh)
	for range 2 {
		if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	_, events := rg.events.list()
	want := domain.Mentions{Targets: []domain.MentionTarget{stamped(minh, created)}, All: true, AllAt: created}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	assertPrevious(t, events[0], want)
	assertPrevious(t, events[1], want)
}
