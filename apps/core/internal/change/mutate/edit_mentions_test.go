package mutate_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	minh = domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}
	lan  = domain.MentionTarget{Kind: domain.MentionUser, ID: "lan"}
	ops  = domain.MentionTarget{Kind: domain.MentionGroup, ID: "ops"}
)

func (rg *rig) sendMentioning(t *testing.T, seq uint64, all bool, targets ...domain.MentionTarget) domain.Message {
	t.Helper()
	m := domain.Message{
		Room: room, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-" + strconv.FormatUint(seq, 10),
		CreatedAt: created, Mentions: targets, MentionAll: all,
	}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert seq %d: %+v", seq, res)
	}
	return m
}

func mentionEdit(base uint32, text string, all bool, targets ...domain.MentionTarget) mutate.EditCmd {
	c := edit("alice", 1, base, text)
	c.Mentions = &mutate.MentionSet{Targets: targets, All: all}
	return c
}

func assertMentioned(t *testing.T, what string, gotTargets []domain.MentionTarget, gotAll bool, all bool, want ...domain.MentionTarget) {
	t.Helper()
	if !slices.Equal(gotTargets, want) || gotAll != all {
		t.Fatalf("%s mentions = %v all=%v, want %v all=%v", what, gotTargets, gotAll, want, all)
	}
}

func TestEditWithMentionsStoresThemInTheFactAndTheMessage(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendMentioning(t, 1, true, minh)
	got, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", false, lan, ops, lan))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	assertMentioned(t, "snapshot", got.Mentions, got.MentionAll, false, lan, ops)
	s := rg.stored(t, 1)
	assertMentioned(t, "stored", s.Mentions, s.MentionAll, false, lan, ops)
	facts := rg.facts(t, 1)
	assertMentioned(t, "fact v1", facts[0].Mentions, facts[0].MentionAll, false, lan, ops)
	original := rg.original(t, 1)
	assertMentioned(t, "original row", original.Mentions, original.MentionAll, true, minh)
	if _, events := rg.events.list(); len(events) != 1 || len(events[0].GetMessageEdited().GetMessage().GetMentionTargets()) != 2 {
		t.Fatalf("events = %v, want msg_edited with the new mentions", events)
	}
}

func TestEditWithoutMentionsKeepsTheCurrentOnes(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendMentioning(t, 1, true, minh, ops)
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1"))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	assertMentioned(t, "snapshot", got.Mentions, got.MentionAll, true, minh, ops)
	facts := rg.facts(t, 1)
	assertMentioned(t, "fact v1", facts[0].Mentions, facts[0].MentionAll, true, minh, ops)
	if _, err := rg.m.Edit(t.Context(), mentionEdit(1, "v2", false)); err != nil {
		t.Fatalf("clearing edit: %v", err)
	}
	s := rg.stored(t, 1)
	assertMentioned(t, "stored after an empty set", s.Mentions, s.MentionAll, false)
}

func TestRetriedEditWithTheSameMentionsInAnyOrderIsARetry(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendMentioning(t, 1, false)
	if _, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", true, minh, ops)); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	for _, c := range []mutate.EditCmd{mentionEdit(0, "v1", true, ops, minh), edit("alice", 1, 0, "v1")} {
		if got, err := rg.m.Edit(t.Context(), c); err != nil || got.Version != 1 {
			t.Fatalf("retry %+v = %+v, %v; want the first result", c.Mentions, got, err)
		}
	}
	for _, c := range []mutate.EditCmd{mentionEdit(0, "v1", false, minh, ops), mentionEdit(0, "v1", true, minh)} {
		if _, err := rg.m.Edit(t.Context(), c); !errors.Is(err, domain.ErrVersionConflict) {
			t.Fatalf("edit %+v at a taken version = %v, want ErrVersionConflict", c.Mentions, err)
		}
	}
	if facts := rg.facts(t, 1); len(facts) != 1 {
		t.Fatalf("facts = %+v, want one", facts)
	}
}

func TestDuplicateVersionWithOtherMentionsIsNotARetry(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendMentioning(t, 1, false)
	rg.m = rg.mutator(t, nil, laggingEdits{rg.edits})
	other := domain.Edit{Room: room, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "v1", At: created, Mentions: []domain.MentionTarget{lan}}
	if err := rg.edits.Append(t.Context(), other); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", false, minh)); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("edit with other mentions = %v, want ErrVersionConflict", err)
	}
	if got, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", false, lan)); err != nil || got.Version != 1 {
		t.Fatalf("same change = %+v, %v; want a retry success", got, err)
	}
}

func TestEditMentioningAllAsksThePolicy(t *testing.T) {
	var asked []access.Action
	noAll := access.PolicyFunc(func(_ context.Context, r access.Request) error {
		asked = append(asked, r.Action)
		if r.Action == access.MentionAll {
			return access.ErrDenied
		}
		return nil
	})
	rg := newRig(t, noAll)
	rg.sendMentioning(t, 1, true)
	if _, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", true)); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("edit with @all = %v, want ErrDenied", err)
	}
	if !slices.Equal(asked, []access.Action{access.EditMessage, access.MentionAll}) || len(rg.facts(t, 1)) != 0 {
		t.Fatalf("asked %v, facts %v; want edit then mention_all and no fact", asked, rg.facts(t, 1))
	}
	if got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1")); err != nil || !got.MentionAll {
		t.Fatalf("edit keeping @all = %+v, %v; want success without asking again", got, err)
	}
}

func TestEditRefusesBadOrTooManyMentions(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	d.Limits.MentionTargets = 2
	rg.m = rg.build(t, d)
	rg.sendMentioning(t, 1, false)
	bad := domain.MentionTarget{Kind: domain.MentionUser, ID: "Bad Id"}
	for _, c := range []mutate.EditCmd{mentionEdit(0, "v1", false, minh, lan, ops), mentionEdit(0, "v1", false, bad)} {
		if _, err := rg.m.Edit(t.Context(), c); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("edit %+v = %v, want ErrInvalidArgument", c.Mentions, err)
		}
	}
	if got, err := rg.m.Edit(t.Context(), mentionEdit(0, "v1", false, minh, lan, minh)); err != nil || len(got.Mentions) != 2 {
		t.Fatalf("edit with a duplicate = %+v, %v; want two targets", got, err)
	}
}
