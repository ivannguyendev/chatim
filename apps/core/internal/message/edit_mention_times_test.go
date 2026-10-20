package message_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func stamped(t domain.MentionTarget, at time.Time) domain.MentionTarget {
	t.At = at
	return t
}

func assertTargetTimes(t *testing.T, what string, got []domain.MentionTarget, want ...domain.MentionTarget) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s targets = %+v, want %+v", what, got, want)
	}
	for i := range want {
		if got[i].Identity() != want[i].Identity() || !got[i].At.Equal(want[i].At) {
			t.Fatalf("%s targets = %+v, want %+v", what, got, want)
		}
	}
}

func (rg *rig) sendStamped(t *testing.T, seq uint64, targets ...domain.MentionTarget) {
	t.Helper()
	m := domain.Message{
		Room: room, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-stamped", CreatedAt: created,
	}
	m = m.WithMentions(domain.Mentions{Targets: targets, All: true}.Stamp(domain.Mentions{}, created))
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert seq %d: %+v", seq, res)
	}
}

func TestEditKeepsTheTimeOfKeptTargetsAndStampsNewOnes(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendStamped(t, 1, minh)
	got, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", true, minh, lan))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	want := []domain.MentionTarget{stamped(minh, created), stamped(lan, rg.at())}
	assertTargetTimes(t, "snapshot", got.Mentions, want...)
	assertTargetTimes(t, "fact v1", rg.facts(t, 1)[0].Mentions, want...)
	if !got.MentionAllAt.Equal(created) || !rg.facts(t, 1)[0].MentionAllAt.Equal(created) {
		t.Fatalf("@all at %v, want the send time %v", got.MentionAllAt, created)
	}
	original := rg.original(t, 1)
	assertTargetTimes(t, "original row", original.Mentions, stamped(minh, created))
	if !original.MentionAllAt.Equal(created) {
		t.Fatalf("original row @all at %v, want %v", original.MentionAllAt, created)
	}
}

func TestATargetDroppedThenAddedAgainGetsTheTimeOfTheReAdd(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendStamped(t, 1, minh)
	if _, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", false, lan)); err != nil {
		t.Fatalf("Edit v1: %v", err)
	}
	rg.now = rg.now.Add(time.Minute)
	got, err := rg.m.Edit(t.Context(), mentionEdit(1, "v2", true, lan, minh))
	if err != nil {
		t.Fatalf("Edit v2: %v", err)
	}
	assertTargetTimes(t, "snapshot v2", got.Mentions, stamped(lan, rg.at().Add(-time.Minute)), stamped(minh, rg.at()))
	if !got.MentionAllAt.Equal(rg.at()) {
		t.Fatalf("@all added again at %v, want %v", got.MentionAllAt, rg.at())
	}
}

func TestRetriedEditIsRecognisedWhateverTheClock(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendStamped(t, 1, minh)
	first, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", true, minh, lan))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	rg.now = rg.now.Add(time.Hour)
	again, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", true, lan, minh))
	if err != nil || again.Version != 1 {
		t.Fatalf("retry = %+v, %v; want the first result", again, err)
	}
	assertTargetTimes(t, "retry snapshot", again.Mentions, first.Mentions...)
	if facts := rg.facts(t, 1); len(facts) != 1 {
		t.Fatalf("facts = %+v, want one", facts)
	}
}

func TestDeleteFactKeepsTheTargetsOfTheDeletedVersion(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendStamped(t, 1, minh)
	if _, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", true, minh, lan)); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	editAt := rg.at()
	rg.now = rg.now.Add(time.Minute)
	got, err := rg.m.Delete(t.Context(), del("alice", 1, 1))
	if err != nil || !got.Deleted || got.Mentions != nil || got.MentionAll || !got.MentionAllAt.IsZero() {
		t.Fatalf("Delete = %+v, %v; want a deleted message without mentions", got, err)
	}
	facts := rg.facts(t, 1)
	if len(facts) != 2 || facts[0].Mentions != nil || facts[0].MentionAll || facts[0].Text != "" {
		t.Fatalf("facts = %+v, want v1 purged and the delete fact", facts)
	}
	gone := facts[1]
	assertTargetTimes(t, "delete fact", gone.Mentions, stamped(minh, created), stamped(lan, editAt))
	if gone.Kind != domain.EditDelete || !gone.MentionAll || !gone.MentionAllAt.Equal(created) {
		t.Fatalf("delete fact = %+v, want the deleted version's @all at %v", gone, created)
	}
	if again, err := rg.m.Delete(t.Context(), del("alice", 1, 1)); err != nil || again.Version != 2 {
		t.Fatalf("retried delete = %+v, %v; want the first result", again, err)
	}
}
