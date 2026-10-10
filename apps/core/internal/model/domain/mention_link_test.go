package domain_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestMentionTargetNamesFollowTheListQuery(t *testing.T) {
	cases := map[string]struct {
		target domain.MentionTarget
		want   string
	}{
		"user":  {user("minh"), "user:minh"},
		"group": {group("team-design"), "group:team-design"},
		"all":   {domain.MentionTarget{Kind: domain.MentionAll}, "all:777"},
	}
	for name, c := range cases {
		if got := c.target.Name(777); got != c.want {
			t.Errorf("%s: Name = %q, want %q", name, got, c.want)
		}
	}
}

func TestAllIsNotASendableMentionTarget(t *testing.T) {
	if _, err := domain.ValidateMentions([]domain.MentionTarget{{Kind: domain.MentionAll}}, 5); err == nil {
		t.Fatal("ValidateMentions accepted an @all target; @all travels as its own flag")
	}
}

func TestMentionTargetsOfAMessageAddAllLast(t *testing.T) {
	m := domain.Message{Mentions: []domain.MentionTarget{user("minh"), group("ops")}, MentionAll: true}
	got := domain.MentionTargetsOf(m)
	want := []domain.MentionTarget{user("minh"), group("ops"), {Kind: domain.MentionAll}}
	if len(got) != len(want) {
		t.Fatalf("MentionTargetsOf = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("MentionTargetsOf = %v, want %v", got, want)
		}
	}
	m.Deleted = true
	if got := domain.MentionTargetsOf(m); len(got) != 0 {
		t.Fatalf("MentionTargetsOf(deleted) = %v, want none", got)
	}
}
